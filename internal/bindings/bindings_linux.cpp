
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

#define MAX_CONNECTIONS 8192

#define QUIC_VERSION    27

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

static uint64_t quicly_idle_timeout_ms = 5 * 1000;
static char quicly_alpn[MAX_CONNECTIONS] = "";
static quicly_conn_t *conns_table[MAX_CONNECTIONS] = {};
static uint64_t requested_cc_algo = QUICLY_CC_RENO;

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

int QuiclyInitializeEngine( uint64_t is_client, const char* alpn, const char* certificate_file, const char* key_file,
      const uint64_t idle_timeout_ms, uint64_t cc_algo )
{
  // update idle timeout
  quicly_idle_timeout_ms = idle_timeout_ms;

  // register the requested CC algorithm
  requested_cc_algo = cc_algo;
  if( cc_algo < 0 || cc_algo >= QUICLY_CC_LAST ) {
    fprintf(stderr, "requested congestion control [%ld] is not available\n", cc_algo);
    return QUICLY_ERROR_UNKNOWN_CC_ALGO;
  }

  // copy requested alpn
  memset( quicly_alpn, '\0', sizeof(char) * MAX_CONNECTIONS );
  strncpy( quicly_alpn, alpn, MAX_CONNECTIONS );

  /* setup quicly context */
  ctx = quicly_spec_context;
  ctx.loss = { (1024/8), 1, 500, 8 };
  ctx.transport_params.max_udp_payload_size = 32768;
  ctx.transport_params.max_idle_timeout = quicly_idle_timeout_ms;
  ctx.transport_params.max_ack_delay = 800;
  ctx.transport_params.max_streams_bidi = MAX_CONNECTIONS;

  ctx.tls = &tlsctx;
  quicly_amend_ptls_context(ctx.tls);
  ctx.stream_open = &stream_open;

  // load certificate
  int ret;
  if ((ret = ptls_load_certificates(&tlsctx, certificate_file)) != 0) {
      fprintf(stderr, "failed to load certificates from file[%d]: %s\n", ret, ERR_error_string(ret, NULL));
      return QUICLY_ERROR_CERT_LOAD_FAILED;
  }

  // client works without key so if not found not a problem
  // on server its necessary
  if( is_client == 1 || key_file == NULL || strlen(key_file) == 0 ) {
    return QUICLY_OK;
  }

  // load private key and associate it to the certificate
  FILE *fp;
  if ((fp = fopen(key_file, "r")) == NULL) {
      fprintf(stderr, "failed to open file:%s:%s\n", key_file, strerror(errno));
      return QUICLY_ERROR_CERT_LOAD_FAILED;
  }
  EVP_PKEY *pkey = PEM_read_PrivateKey(fp, NULL, NULL, NULL);
  fclose(fp);
  if (pkey == NULL) {
      fprintf(stderr, "failed to load private key from file:%s\n", key_file);
      return QUICLY_ERROR_CERT_LOAD_FAILED;
  }

  ptls_openssl_sign_certificate_t* sign_certificate = (ptls_openssl_sign_certificate_t*)malloc( sizeof(ptls_openssl_sign_certificate_t) );
  ptls_openssl_init_sign_certificate(sign_certificate, pkey);
  EVP_PKEY_free(pkey);
  tlsctx.sign_certificate = &sign_certificate->super;

  // check consistency
  if ((tlsctx.certificates.count != 0) != (tlsctx.sign_certificate != NULL)) {
    return QUICLY_ERROR_CERT_LOAD_FAILED;
  }

  return QUICLY_OK;
}

int QuiclyCloseEngine() {
  return QUICLY_OK;
}

static int apply_requested_cc_algo(quicly_conn_t *conn)
{
  int ret = QUICLY_OK;
  if( conn == NULL )
    return QUICLY_ERROR_FAILED;

  switch( requested_cc_algo ) {
    case QUICLY_CC_RENO:
      quicly_set_cc(conn, &quicly_cc_type_reno);
      break;
    case QUICLY_CC_CUBIC:
      quicly_set_cc(conn, &quicly_cc_type_cubic);
      break;
    case QUICLY_CC_PICO:
      quicly_set_cc(conn, &quicly_cc_type_pico);
      break;
    case QUICLY_CC_SEARCH:
      quicly_set_cc(conn, &quicly_cc_type_search);
      break;
    default:
      ret = QUICLY_ERROR_UNKNOWN_CC_ALGO;
  }

  return ret;
}

// ----- Callbacks ----- //

static int on_client_hello_cb(ptls_on_client_hello_t *_self, ptls_t *tls, ptls_on_client_hello_parameters_t *params)
{
    int ret;

    // checks the application protocol extension sent by the client, ok if finds the expected one from
    // the server, error if none match or empty
    size_t i, j;
    size_t alpn_len = strlen(quicly_alpn);
    //printf("stream requested protocol: %s\n", quicly_alpn);
    const ptls_iovec_t *y;
    for (j = 0; j != params->negotiated_protocols.count; ++j) {
        y = params->negotiated_protocols.list + j;
        //printf(">> protocol check: %s == %s\n", quicly_alpn, y->base);
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

    //printf("stream opened: %lld\n", stream->stream_id);

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
    printf( "1. stream %lld closed, err: %d\n", stream->stream_id, err );

    // callback to go code
    const quicly_cid_plaintext_t* cid = quicly_get_master_id(stream->conn);
    printf( "2. stream %lld closed, err: %d\n", stream->stream_id, err );
    goQuiclyOnStreamClose( uint64_t(cid->master_id), uint64_t(stream->stream_id), err );
    printf( "3. stream %lld closed, err: %d\n", stream->stream_id, err );

    quicly_streambuf_destroy(stream, err);
    printf( "4. stream %lld closed, err: %d\n", stream->stream_id, err );
}

static void on_stop_sending(quicly_stream_t *stream, int err)
{
    printf("received STOP_SENDING: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    quicly_close(stream->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(err), "");
}

static void on_receive_reset(quicly_stream_t *stream, int err)
{
    printf("received RESET_STREAM: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    quicly_close(stream->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(err), "");
}

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len)
{
    //printf("received PACKET: %lld\n", len);

    /* read input to receive buffer */
    if (quicly_streambuf_ingress_receive(stream, off, src, len) != 0) {
        return;
    }

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
    // Address resolution
    struct in_addr byte_addr;
    byte_addr.s_addr = inet_addr(_address);

    struct sockaddr_in address{
      .sin_family = AF_INET,
      .sin_port = htons(port),
      .sin_addr = byte_addr
    };

    int i;
    for (i = 0; i < MAX_CONNECTIONS; ++i)
    {
      if( conns_table[i] == NULL )
        break;
    }
    if( i > MAX_CONNECTIONS-1 )
      return QUICLY_ERROR_FAILED;

    // Version used
    ctx.transport_params.max_idle_timeout = quicly_idle_timeout_ms;

    // Application protocol extension
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
        return ret;
    }

    if( (ret = apply_requested_cc_algo(conns_table[i]) ) != 0 )
    {
        fprintf(stderr, "apply_requested_cc_algo failed:%d\n", ret);
        return ret;
    }

    return QUICLY_OK;
}

int QuiclyClose( size_t conn_id, int error )
{
    if( conn_id > MAX_CONNECTIONS-1 || conns_table[conn_id] == NULL ) {
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

        if (quicly_decode_packet(&ctx, decoded, (const uint8_t *)msg, dgram_len, &off) == SIZE_MAX) {
            err = QUICLY_ERROR_DECODE_FAILED;
            break;
        }

        /* find the corresponding connection */
        for (i = 0; i < MAX_CONNECTIONS && conns_table[i] != NULL; ++i)
            if (quicly_is_destination(conns_table[i], NULL, (struct sockaddr*)&address, decoded)) {
                break;
            }
        if( i >= MAX_CONNECTIONS ) {
            err = QUICLY_ERROR_DESTINATION_NOT_FOUND;
            break;
        }

        int ret = 0;
        if (conns_table[i] != NULL) {
            /* let the current connection handle ingress packets */
            ret = quicly_receive(conns_table[i], NULL, (struct sockaddr*)&address, decoded);
            //printf("receive err: %d\n", ret);

        } else if (!is_client) {
            if( id != NULL ) {
              *id = i;
            }
            /* assume that the packet is a new connection */
            ret = quicly_accept(conns_table + *id, &ctx, NULL, (struct sockaddr*)&address, decoded, NULL, &next_cid, NULL, NULL);
            //printf("accept err: %d\n", ret);
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
    size_t buf_size = 32 * ctx.transport_params.max_udp_payload_size * sizeof(uint8_t);
    static uint8_t* dgrams_buf = (uint8_t*)malloc( buf_size+1 ); //[(*num_dgrams) * ctx.transport_params.max_udp_payload_size];
    memset( dgrams_buf, '\0', buf_size+1 );

    if( conns_table[id] == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    int ret = quicly_send(conns_table[id], &dest, &src, dgrams_out, num_dgrams, dgrams_buf, buf_size);

    //free( dgrams_buf );

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
        //printf("quicly_send returned %d, QUICLY_ERROR_FREE_CONNECTION\n", ret);
        quicly_free(conns_table[id]);
        conns_table[id] = NULL;
        return QUICLY_ERROR_NOT_OPEN;

    default:
        //printf("quicly_send returned %d\n", ret);
        return QUICLY_ERROR_FAILED;
    }

    return QUICLY_OK;
}


// ----- Stream ----- //

int QuiclyOpenStream( size_t conn_id, size_t* stream_id )
{
    if( conn_id > MAX_CONNECTIONS-1 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_NOT_OPEN;
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
    if( conn_id > MAX_CONNECTIONS-1 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_NOT_OPEN;
    }

    quicly_stream_t *stream = quicly_get_stream(conns_table[conn_id], stream_id);
    if( stream == NULL ) {
        return QUICLY_ERROR_STREAM_NOT_FOUND;
    }

    quicly_streambuf_egress_shutdown(stream);

    quicly_streambuf_destroy(stream, error);
    return QUICLY_OK;
}

int QuiclyWriteStream( size_t conn_id, size_t stream_id, char* msg, size_t dgram_len )
{
    if( conn_id > MAX_CONNECTIONS-1 || conns_table[conn_id] == NULL ) {
        return QUICLY_ERROR_NOT_OPEN;
    }

    quicly_stream_t *stream = quicly_get_stream(conns_table[conn_id], stream_id);
    if( stream == NULL ) {
        return QUICLY_ERROR_STREAM_NOT_FOUND;
    }

    if (quicly_sendstate_is_open(&stream->sendstate) && (dgram_len > 0)) {
        quicly_streambuf_egress_write(stream, msg, dgram_len);
    }

    return QUICLY_OK;
}

#endif
