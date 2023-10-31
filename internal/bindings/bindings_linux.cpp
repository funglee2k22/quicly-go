
#ifdef UNIX

extern "C" {
  #include "quicly_wrapper.h"
  #include "quicly.h"
  #include "quicly/streambuf.h"
  #include "quicly/defaults.h"
  #include "picotls/openssl.h"
  #include "openssl/pem.h"
  #include "openssl/err.h"

  #include <arpa/inet.h>
}

#define SERVER_CERT  "server_cert.pem"
#define SERVER_KEY   "server_key.pem"

static int on_stream_open(quicly_stream_open_t *self, quicly_stream_t *stream);
static void on_destroy(quicly_stream_t *stream, int err);

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len);

static void on_stop_sending(quicly_stream_t *stream, int err);
static void on_receive_reset(quicly_stream_t *stream, int err);

static int on_client_hello_cb(ptls_on_client_hello_t *_self, ptls_t *tls, ptls_on_client_hello_parameters_t *params);

/**
 * the QUIC context
 */
static quicly_context_t ctx;
/**
 * CID seed
 */
static quicly_cid_plaintext_t next_cid;

static char quicly_alpn[256] = "";
static quicly_conn_t *conns_table[256] = {};

static quicly_stream_open_t stream_open = { on_stream_open };
static ptls_on_client_hello_t on_client_hello = {on_client_hello_cb};

static ptls_context_t tlsctx = {
 .random_bytes = ptls_openssl_random_bytes,
 .get_time = &ptls_get_time,
 .key_exchanges = ptls_openssl_key_exchanges,
 .cipher_suites = ptls_openssl_cipher_suites,
 .on_client_hello = &on_client_hello,
};

// ----- Startup ----- //

int QuiclyInitializeEngine( const char* alpn, const char* certificate_file, const char* key_file ) {
  // copy requested alpn
  memset( quicly_alpn, '\0', sizeof(char) * 256 );
  strncpy( quicly_alpn, alpn, 256 );

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

static int on_client_hello_cb(ptls_on_client_hello_t *_self, ptls_t *tls, ptls_on_client_hello_parameters_t *params)
{
    int ret;

    size_t i, j;
    size_t alpn_len = strlen(quicly_alpn);
    const ptls_iovec_t *y;
    for (j = 0; j != params->negotiated_protocols.count; ++j) {
        y = params->negotiated_protocols.list + j;
        if (alpn_len == y->len && memcmp(quicly_alpn, y->base, alpn_len) == 0) {
          ret = ptls_set_negotiated_protocol(tls, (const char *)quicly_alpn, alpn_len);
          return ret;
        }
    }
    return PTLS_ALERT_NO_APPLICATION_PROTOCOL;
}

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

    printf("stream opened: %ld\n", stream->stream_id);

    if ((ret = quicly_streambuf_create(stream, sizeof(quicly_streambuf_t))) != 0)
        return ret;
    stream->callbacks = &stream_callbacks;

    // callback to go code
    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    goQuiclyOnStreamOpen( uint64_t(cid->master_id), uint64_t(stream->stream_id) );

    return 0;
}

static void on_destroy(quicly_stream_t *stream, int err)
{
    printf( "\nstream %ld closed, err: %d\n", stream->stream_id, err );

    // callback to go code
    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    goQuiclyOnStreamClose( uint64_t(cid->master_id), uint64_t(stream->stream_id), err );

    quicly_streambuf_destroy(stream, err);
}

static void on_stop_sending(quicly_stream_t *stream, int err)
{
    printf("\nreceived STOP_SENDING: %ld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    quicly_close(stream->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(0), "");
}

static void on_receive_reset(quicly_stream_t *stream, int err)
{
    printf("\nreceived RESET_STREAM: %ld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
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
    if( i > 255 )
      return QUICLY_ERROR_FAILED;

    ctx.transport_params.max_udp_payload_size = 8192;
    ctx.transport_params.max_idle_timeout = 3 * 1000;
    ctx.transport_params.max_streams_bidi = 256;

    ptls_iovec_t proposed_alpn[] = {
      { (uint8_t *)quicly_alpn, strlen(quicly_alpn) }
    };
    ptls_handshake_properties_t client_hs_prop = {
      .client = {
        .negotiated_protocols = {
          .list = proposed_alpn,
          .count = 1,
        }
      }
    };

    /* initiate a connection, and open a stream */
    int ret = 0;
    if ((ret = quicly_connect(conns_table + i, &ctx,
                              _address, (struct sockaddr *)&address, NULL,
                              &next_cid, ptls_iovec_init(NULL, 0),
                              &client_hs_prop,
                              NULL, NULL)) != 0)
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

    int err = QUICLY_OK;
    quicly_decoded_packet_t* decoded = NULL;
    /* split UDP datagram into multiple QUIC packets */
    while (off < dgram_len) {
        free(decoded);
        decoded = NULL;
        decoded = (quicly_decoded_packet_t*)malloc( sizeof(quicly_decoded_packet_t) );

        if (quicly_decode_packet(&ctx, decoded, (const uint8_t *)msg, dgram_len-off, &off) == SIZE_MAX) {
            err = QUICLY_ERROR_FAILED;
            break;
        }

        /* find the corresponding connection */
        for (i = 0; i < 256 && conns_table[i] != NULL; ++i)
            if (quicly_is_destination(conns_table[i], NULL, (struct sockaddr*)&address, decoded)) {
                break;
            }
        if( i >= 256 ) {
            err = QUICLY_ERROR_FAILED;
        }

        int ret = 0;
        if (conns_table[i] != NULL) {
            /* let the current connection handle ingress packets */
            ret = quicly_receive(conns_table[i], NULL, (struct sockaddr*)&address, decoded);

        } else if (!is_client) {
            if( id != NULL ) {
              *id = i;
            }
            /* assume that the packet is a new connection */
            ret = quicly_accept(conns_table + *id, &ctx, NULL, (struct sockaddr*)&address, decoded, NULL, &next_cid, NULL, NULL);
        }
        if( ret != 0 ) {
          *id = 0;
          err = ret;
          break;
        }
    }
    free(decoded);
    decoded = NULL;

    return err;
}

int QuiclyOutgoingMsgQueue( size_t id, struct iovec* dgrams_out, size_t* num_dgrams )
{
    quicly_address_t dest, src;
    uint8_t dgrams_buf[(*num_dgrams) * ctx.transport_params.max_udp_payload_size];

    if( conns_table[id] == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    int ret = quicly_send(conns_table[id], &dest, &src, dgrams_out, num_dgrams, dgrams_buf, sizeof(dgrams_buf));

    switch (ret) {
    case 0:
      break;
//      case 0: {
//          size_t j;
//          for (j = 0; j != *num_dgrams; ++j) {
//              //send_one(fd, &dest.sa, &dgrams[j]);
//              printf("packet %p %d\n", dgrams_out[j].iov_base, dgrams_out[j].iov_len);
//          }
//      } break;
    case QUICLY_ERROR_FREE_CONNECTION:
        /* connection has been closed, free, and exit when running as a client */
        printf("quicly_send returned %d, QUICLY_ERROR_FREE_CONNECTION\n", ret);
        quicly_free(conns_table[id]);
        conns_table[id] = NULL;
        break;
    default:
        printf("quicly_send returned %d\n", ret);
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
