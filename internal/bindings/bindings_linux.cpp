
#ifdef UNIX

extern "C" {
  #include "quicly_wrapper.h"
  #include "quicly.h"
  #include "quicly/streambuf.h"
  #include "quicly/defaults.h"
  #include "picotls/openssl.h"
  #include "openssl/pem.h"
  #include "openssl/err.h"
}

#define SERVER_CERT  "server_cert.pem"
#define SERVER_KEY   "server_key.pem"

static int on_stream_open(quicly_stream_open_t *self, quicly_stream_t *stream);
static void on_destroy(quicly_stream_t *stream, int err);

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len);

static void on_stop_sending(quicly_stream_t *stream, int err);
static void on_receive_reset(quicly_stream_t *stream, int err);

/**
 * the QUIC context
 */
static quicly_context_t ctx;
/**
 * CID seed
 */
static quicly_cid_plaintext_t next_cid;

static quicly_conn_t *conns_table[256] = {};

static ptls_context_t tlsctx = {
 .random_bytes = ptls_openssl_random_bytes,
 .get_time = &ptls_get_time,
 .key_exchanges = ptls_openssl_key_exchanges,
 .cipher_suites = ptls_openssl_cipher_suites,
};

static quicly_stream_open_t stream_open = { on_stream_open };

// ----- Startup ----- //

int QuiclyInitializeEngine( const char* certificate_file, const char* key_file ) {
  WSADATA wsaData;
  WORD wVersionRequested = MAKEWORD(2, 2);
  int err = WSAStartup(wVersionRequested, &wsaData);
  if( err != 0 || certificate_file == NULL || strlen(certificate_file) == 0 ) {
      printf("WSAStartup failed with error: %d\n", err);
      return QUICLY_ERROR_FAILED;
  }

//  /* setup quic context */
  ctx = quicly_spec_context;
  ctx.tls = &tlsctx;
  quicly_amend_ptls_context(ctx.tls);
  ctx.stream_open = &stream_open;

  // load certificate
  int ret;
  if ((ret = ptls_load_certificates(&tlsctx, certificate_file)) != 0) {
      fprintf(stderr, "failed to load certificates from file[%d]: %s\n", ret, ERR_error_string(ret, NULL));
      return QUICLY_ERROR_FAILED;
  }

  if( key_file == NULL || strlen(key_file) == 0 ) {
    return QUICLY_OK;
  }

  // load private key and associate it to the certificate
  FILE *fp;
  if ((fp = fopen(SERVER_KEY, "r")) == NULL) {
      fprintf(stderr, "failed to open file:%s:%s\n", SERVER_KEY, strerror(errno));
      exit(1);
  }
  EVP_PKEY *pkey = PEM_read_PrivateKey(fp, NULL, NULL, NULL);
  fclose(fp);
  if (pkey == NULL) {
      fprintf(stderr, "failed to load private key from file:%s\n", SERVER_KEY);
      exit(1);
  }

  ptls_openssl_sign_certificate_t* sign_certificate = (ptls_openssl_sign_certificate_t*)malloc( sizeof(ptls_openssl_sign_certificate_t) );
  ptls_openssl_init_sign_certificate(sign_certificate, pkey);
  EVP_PKEY_free(pkey);
  tlsctx.sign_certificate = &sign_certificate->super;

  // check consistency
  if ((tlsctx.certificates.count != 0) != (tlsctx.sign_certificate != NULL)) {
    return QUICLY_ERROR_FAILED;
  }

  return QUICLY_OK;
}

int QuiclyCloseEngine() {
  return QUICLY_OK;
}

// ----- Callbacks ----- //

static const quicly_stream_callbacks_t stream_callbacks = {
    on_destroy,
    quicly_streambuf_egress_shift,
    quicly_streambuf_egress_emit,
    on_stop_sending,
    on_receive,
    on_receive_reset};

static int on_stream_open(quicly_stream_open_t *self, quicly_stream_t *stream)
{
    int ret;

    printf("stream opened: %lld\n", stream->stream_id);

    if ((ret = quicly_streambuf_create(stream, sizeof(quicly_streambuf_t))) != 0)
        return ret;
    printf("echo.c@%d\n", __LINE__ );
    stream->callbacks = &stream_callbacks;

    // callback to go code
    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    goQuiclyOnStreamOpen( uint64_t(cid->master_id), uint64_t(stream->stream_id) );

    return 0;
}

static void on_destroy(quicly_stream_t *stream, int err)
{
    printf( "stream %lld closed, err: %d\n", stream->stream_id, err );

    // callback to go code
    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    goQuiclyOnStreamClose( uint64_t(cid->master_id), uint64_t(stream->stream_id), err );

    quicly_streambuf_destroy(stream, err);
}

static void on_stop_sending(quicly_stream_t *stream, int err)
{
    printf("received STOP_SENDING: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    quicly_close(stream->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(0), "");
}

static void on_receive_reset(quicly_stream_t *stream, int err)
{
    printf("received RESET_STREAM: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    quicly_close(stream->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(0), "");
}

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len)
{
    /* read input to receive buffer */
    if (quicly_streambuf_ingress_receive(stream, off, src, len) != 0)
        return;

    /* obtain contiguous bytes from the receive buffer */
    ptls_iovec_t input = quicly_streambuf_ingress_get(stream);

    struct iovec vec = {
      .iov_base = (char*)input.base,
      .iov_len  = input.len,
    };

    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    goQuiclyOnStreamReceived(uint64_t(cid->master_id), uint64_t(stream->stream_id), &vec);

    /* remove used bytes from receive buffer */
    quicly_streambuf_ingress_shift(stream, input.len);
}

// ----- Connection ----- //
int QuiclyConnect( const char* _address, int port, size_t* id )
{
    struct in_addr byte_addr;
    byte_addr.s_addr = inet_addr(_address);

    struct sockaddr_in address{
      .sin_family = AF_INET,
      .sin_port = htons(port),
      .sin_addr = byte_addr
    };

    int i;
    for (i = 0; i < 256; ++i)
    {
      if( conns_table[i] == NULL )
        break;
    }

    /* initiate a connection, and open a stream */
    int ret = 0;
    if ((ret = quicly_connect(conns_table + i, &ctx,
                              _address, (struct sockaddr *)&address, NULL,
                              &next_cid, ptls_iovec_init(NULL, 0), NULL, NULL, NULL)) != 0)
    {
        fprintf(stderr, "quicly_connect failed:%d\n", ret);
        return QUICLY_ERROR_FAILED;
    }

    return QUICLY_OK;
}

int QuiclyClose( size_t conn_id, int error )
{
    if( conn_id > 255 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    int ret = quicly_close(conns_table[conn_id], QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(error), "");
    conns_table[conn_id] = NULL;
    return ret;
}

int QuiclyProcessMsg( int is_client, const char* _address, int port, char* msg, size_t dgram_len, size_t* id )
{
    size_t off = 0, i = 0;

    struct in_addr byte_addr;
    byte_addr.s_addr = inet_addr(_address);

    struct sockaddr_in address{
      .sin_family = AF_INET,
      .sin_port = htons(port),
      .sin_addr = byte_addr
    };

    /* split UDP datagram into multiple QUIC packets */
    while (off < dgram_len) {
        quicly_decoded_packet_t decoded;
        if (quicly_decode_packet(&ctx, &decoded, (const uint8_t *)msg, dgram_len, &off) == SIZE_MAX)
            return QUICLY_ERROR_FAILED;

        /* find the corresponding connection */
        for (i = 0; i < 256 && conns_table[i] != NULL; ++i)
            if (quicly_is_destination(conns_table[i], NULL, (struct sockaddr*)&address, &decoded)) {
                break;
            }
        int ret = 0;
        if (conns_table[i] != NULL) {
            /* let the current connection handle ingress packets */
            ret = quicly_receive(conns_table[i], NULL, (struct sockaddr*)&address, &decoded);

        } else if (!is_client) {
            if( id != NULL ) {
              *id = i;
            }
            /* assume that the packet is a new connection */
            ret = quicly_accept(conns_table + *id, &ctx, NULL, (struct sockaddr*)&address, &decoded, NULL, &next_cid, NULL, NULL);
        }
        if( ret != 0 ) {
          *id = 0;
          return ret;
        }
    }

    return QUICLY_OK;
}

int QuiclyOutgoingMsgQueue( size_t id, struct iovec* dgrams_out, size_t* num_dgrams )
{
    quicly_address_t dest, src;
    uint8_t dgrams_buf[(*num_dgrams) * ctx.transport_params.max_udp_payload_size];

    int ret = quicly_send(conns_table[id], &dest, &src, dgrams_out, num_dgrams, dgrams_buf, sizeof(dgrams_buf));

    switch (ret) {
//    case 0:
//      break;
      case 0: {
          size_t j;
          for (j = 0; j != *num_dgrams; ++j) {
              //send_one(fd, &dest.sa, &dgrams[j]);
              printf("packet %p %d\n", dgrams_out[j].iov_base, dgrams_out[j].iov_len);
          }
      } break;
    case QUICLY_ERROR_FREE_CONNECTION:
        /* connection has been closed, free, and exit when running as a client */
        quicly_free(conns_table[id]);
        conns_table[id] = NULL;
        break;
    default:
        fprintf(stderr, "quicly_send returned %d\n", ret);
        return QUICLY_ERROR_FAILED;
    }

    return QUICLY_OK;
}


// ----- Stream ----- //

int QuiclyOpenStream( size_t conn_id, size_t* stream_id )
{
    if( conn_id > 255 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    quicly_conn_t* client = conns_table[conn_id];

    quicly_stream_t *stream = NULL;
    int ret = quicly_open_stream(client, &stream, 0);
    if( ret == QUICLY_OK ) {
      *stream_id = stream->stream_id;
    }
    return ret;
}

int QuiclyCloseStream( size_t conn_id, size_t stream_id, int error )
{
    if( conn_id > 255 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    quicly_stream_t *stream = quicly_get_stream(conns_table[conn_id], stream_id);
    if( stream == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    quicly_streambuf_egress_shutdown(stream);

    return QUICLY_OK;
}

int QuiclyWriteStream( size_t conn_id, size_t stream_id, char* msg, size_t dgram_len )
{
    if( conn_id > 255 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    quicly_stream_t *stream = quicly_get_stream(conns_table[conn_id], stream_id);
    if( stream == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    if (quicly_sendstate_is_open(&stream->sendstate) && (dgram_len > 0)) {
        quicly_streambuf_egress_write(stream, msg, dgram_len);
    }

    return QUICLY_OK;
}

#endif
