
#ifdef DARWIN

extern "C" {
#include "quicly_wrapper.h"
#include "quicly.h"
#include "quicly/streambuf.h"
#include "quicly/defaults.h"
#include "picotls/openssl.h"
#include "openssl/pem.h"
#include "openssl/err.h"

#include <arpa/inet.h>
#include <sys/types.h>
#include <netinet/in.h>
#include <unistd.h>
#include "hashmap.h"
}

#include <mutex>
#include <thread>

static std::mutex global_lock;

#define MAX_CONNECTIONS 1024
#define QUIC_VERSION    (0xff000000 | 29)

static uint64_t global_trace_on = 0;

static int on_stream_open(quicly_stream_open_t *self, quicly_stream_t *stream);
static void on_destroy(quicly_stream_t *stream, int err);
static void on_connection_close(quicly_closed_by_remote_t *self, quicly_conn_t *conn, int err, uint64_t frame_type,
                                const char *reason, size_t reason_len);

static void print_ranges( const char* prefix, quicly_ranges_t* ranges_st );

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len);
static void on_sent_bytes(quicly_stream_t *stream, size_t off, void *dst, size_t *len, int *wrote_all);

static void on_stop_sending(quicly_stream_t *stream, int err);
static void on_receive_reset(quicly_stream_t *stream, int err);
static void on_acked_sent_bytes(quicly_stream_t *stream, size_t delta);
static void on_receive_datagram_frame(quicly_receive_datagram_frame_t *self, quicly_conn_t *conn, ptls_iovec_t payload);

static void quiclygo_quic_tracer(void *ctx, const char *fmt, ...) {
  if( !global_trace_on )
    return;

  va_list args;
  va_start (args, fmt);
  vprintf (fmt, args);
  va_end (args);
}

static int on_client_hello_cb(ptls_on_client_hello_t *_self, ptls_t *tls, ptls_on_client_hello_parameters_t *params);

static int quiclygo_stream_scheduler_can_send(quicly_stream_scheduler_t *self, quicly_conn_t *conn, int conn_is_saturated);
static int quiclygo_stream_scheduler_update_state(quicly_stream_scheduler_t *self, quicly_stream_t *stream);
static int quiclygo_stream_scheduler_do_send(quicly_stream_scheduler_t *self, quicly_conn_t *conn, quicly_send_context_t *s);

/**
 * the QUIC context
 */
static quicly_context_t ctx;
/**
 * CID seed
 */
static quicly_cid_plaintext_t next_cid;

static uint64_t quicly_idle_timeout_ms = 60 * 1000;
static char quicly_alpn[MAX_CONNECTIONS] = "";

struct quiclygo_conn_entry {
  uint64_t uuid;
  quicly_conn_t * conn;
};
static struct hashmap *connections_map = NULL;

static uint64_t requested_cc_algo = QUICLY_CC_RENO;

static quicly_stream_open_t stream_open = { on_stream_open };
static quicly_closed_by_remote_t connection_closed = { on_connection_close };
static ptls_on_client_hello_t on_client_hello = {on_client_hello_cb};
static ptls_key_exchange_algorithm_t *quiclygo_openssl_key_exchanges[] = {&ptls_openssl_x25519, NULL};
static ptls_cipher_suite_t *quiclygo_openssl_cipher_suites[] = {&ptls_openssl_aes128gcmsha256, NULL};

static ptls_context_t tlsctx = {
 .random_bytes = ptls_openssl_random_bytes,
 .get_time = &ptls_get_time,
 .key_exchanges = quiclygo_openssl_key_exchanges,
 .cipher_suites = quiclygo_openssl_cipher_suites,
 .on_client_hello = &on_client_hello,
};

static quicly_receive_datagram_frame_t receive_dgram = {on_receive_datagram_frame};

static quicly_tracer_t qtracer = {
 .cb = &quiclygo_quic_tracer,
 .ctx = NULL,
};

static quicly_stream_scheduler_t quicly_quiclygo_stream_scheduler = {quiclygo_stream_scheduler_can_send, quiclygo_stream_scheduler_do_send,
                                                             quiclygo_stream_scheduler_update_state};

const quicly_context_t quiclygo_context = {NULL,                                                 /* tls */
                                              1280,          /* client_initial_size */
                                              { (1024 / 8), 500, 500, 2 },                                /* loss */
                                              {{30 * 1024 * 1472, 30 * 1024 * 1472, 30 * 1024 * 1472}, /* max_stream_data */
                                               30 * 1024 * 1472,                                    /* max_data */
                                               60 * 1000,                                           /* idle_timeout (30 seconds) */
                                               1024, /* max_concurrent_streams_bidi */
                                               0,   /* max_concurrent_streams_uni */
                                               65527 }, // DEFAULT_MAX_UDP_PAYLOAD_SIZE},
                                              16777216, //DEFAULT_MAX_PACKETS_PER_KEY,
                                              65535, // DEFAULT_MAX_CRYPTO_BYTES,
                                              1028, // DEFAULT_INITCWND_PACKETS,
                                              QUIC_VERSION, // protocol version 29,
                                              4096, // DEFAULT_PRE_VALIDATION_AMPLIFICATION_LIMIT,
                                              64, /* ack_frequency */
                                              400, // DEFAULT_HANDSHAKE_TIMEOUT_RTT_MULTIPLIER,
                                              10, // DEFAULT_MAX_INITIAL_HANDSHAKE_PACKETS,
                                              5, // DEFAULT_MAX_PROBE_PACKETS
                                              100, // DEFAULT_MAX_PATH_VALIDATION_FAILURES
                                              1280 * 1000, // default_jumpstart_cwnd_packets
                                              1280 * 1000, // max_jumpstart_cwnd_packets
                                              0, /* enlarge_client_hello */
                                              0, // enable_ecn,
                                              0, // use_pacing,
                                              0, // respect_app_limited,
                                              NULL, // cid_encryptor
                                              &stream_open, /* on_stream_open */
                                              &quicly_quiclygo_stream_scheduler,
                                              &receive_dgram, /* receive_datagram_frame */
                                              &connection_closed, /* on_conn_close */
                                              &quicly_default_now,
                                              NULL, // save_resumption_token
                                              NULL, // generate_resumption_token
                                              &quicly_default_crypto_engine,
                                              &quicly_default_init_cc,
                                              NULL, // update_open_count
                                              NULL  // async_handshake
                                       };

#define TRACE(msg, ...)\
  if( global_trace_on != 0 ) { printf(msg, ##__VA_ARGS__); }\


// ----- connections hashmap ----- //
static int quiclygo_conn_entry_compare(const void *a, const void *b, void *udata) {
    const struct quiclygo_conn_entry *entrya = (const struct quiclygo_conn_entry *)a;
    const struct quiclygo_conn_entry *entryb = (const struct quiclygo_conn_entry *)b;
    return entrya->uuid == entryb->uuid ? 0 : 1;
}

static uint64_t quiclygo_conn_entry_hash(const void *item, uint64_t seed0, uint64_t seed1) {
    const struct quiclygo_conn_entry *e = (const struct quiclygo_conn_entry *)item;
    return e->uuid;
}

static uint64_t get_connection_uuid( quicly_conn_t* conn )
{
  if( conn == NULL )
    return 0;

  size_t iter = 0;
  void *item = NULL;
  while (hashmap_iter(connections_map, &iter, &item)) {
    const struct quiclygo_conn_entry *econn = (const struct quiclygo_conn_entry *)item;

    if ( econn->conn == conn ) {
      return econn->uuid;
    }
  }
  return 0;
}

static quicly_conn_t* get_connection( uint64_t conn_id )
{
  struct quiclygo_conn_entry e = { .uuid=conn_id };
  struct quiclygo_conn_entry *econn = (struct quiclygo_conn_entry*)hashmap_get( connections_map, &e );
  if( econn == NULL ) {
    TRACE(">> GET failed: %lld\n", conn_id);
    return NULL;
  }

  quicly_conn_t* res = econn->conn;
  return res;
}

static void add_connection( uint64_t conn_id, quicly_conn_t* conn )
{
  TRACE(">> ADD: %lld - %p\n", conn_id, conn);
  struct quiclygo_conn_entry e = { .uuid=conn_id, .conn=conn };
  hashmap_set( connections_map, &e );
}

static void delete_connection( uint64_t conn_id )
{
  TRACE(">> DELETE: %lld\n", conn_id);
  struct quiclygo_conn_entry e = { .uuid=conn_id };
  hashmap_delete( connections_map, &e );
}

// ----- Startup ----- //

int QuiclyInitializeEngine( uint64_t is_client, const char* alpn, const char* certificate_file, const char* key_file,
      const uint64_t idle_timeout_ms, uint64_t cc_algo, uint64_t ss_algo, uint64_t trace_quicly )
{

std::lock_guard<std::mutex> lock(global_lock);

  global_trace_on = trace_quicly;

  // connections hashmap setup
  connections_map = hashmap_new(sizeof(struct quiclygo_conn_entry), 0, 0, 0,
                               quiclygo_conn_entry_hash, quiclygo_conn_entry_compare, NULL, NULL);

  // update idle timeout
  quicly_idle_timeout_ms = idle_timeout_ms;

  // copy requested alpn
  memset( quicly_alpn, '\0', sizeof(char) * MAX_CONNECTIONS );
  strncpy( quicly_alpn, alpn, MAX_CONNECTIONS );

  /* setup quicly context */
  ctx = quiclygo_context;
  ctx.transport_params.max_idle_timeout = quicly_idle_timeout_ms;

  // register the requested CC algorithm
  switch( cc_algo ) {
    case QUICLY_CC_RENO:
      ctx.init_cc = &quicly_cc_reno_init;
      break;
    case QUICLY_CC_CUBIC:
      ctx.init_cc = &quicly_cc_cubic_init;
      break;
    case QUICLY_CC_PICO:
      ctx.init_cc = &quicly_cc_pico_init;
      break;
    default:
      break;
  }

  // register the requested SS algorithm
  switch( ss_algo ) {
    case QUICLY_SS_SEARCH:
      ctx.cc_slowstart = &quicly_ss_type_search;
      break;
    case QUICLY_SS_DISABLED:
      ctx.cc_slowstart = &quicly_ss_type_disabled;
      break;
    case QUICLY_SS_RFC2001:
      ctx.cc_slowstart = &quicly_ss_type_rfc2001;
      break;
    // leave default for the type
    default:
      break;
  }

  ctx.tls = &tlsctx;
  quicly_amend_ptls_context(ctx.tls);

  // load certificate
  int ret;
  if ((ret = ptls_load_certificates(&tlsctx, certificate_file)) != 0) {
      TRACE("failed to load certificates from file[%d]: %s\n\n", ret, ERR_error_string(ret, NULL));
      return QUICLY_ERROR_CERT_LOAD_FAILED;
  }

  // adds quicly tracing output to stdout
  if( global_trace_on != 0 ) {
    ptls_log_add_fd(2);
  }

  // client works without key so if not found not a problem
  // on server its necessary
  if( is_client == 1 || key_file == NULL || strlen(key_file) == 0 ) {
    return QUICLY_OK;
  }

  // load private key and associate it to the certificate
  FILE *fp;
  if ((fp = fopen(key_file, "r")) == NULL) {
      TRACE("failed to open file:%s:%s\n\n", key_file, strerror(errno));
      return QUICLY_ERROR_CERT_LOAD_FAILED;
  }
  EVP_PKEY *pkey = PEM_read_PrivateKey(fp, NULL, NULL, NULL);
  fclose(fp);
  if (pkey == NULL) {
      TRACE("failed to load private key from file:%s\n\n", key_file);
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

std::lock_guard<std::mutex> lock(global_lock);

  global_trace_on = 0;

  hashmap_free( connections_map );
  connections_map = NULL;

  TRACE(">> Closed Quicly-Go backend\n");
  return QUICLY_OK;
}

// ----- Callbacks ----- //

static int on_client_hello_cb(ptls_on_client_hello_t *_self, ptls_t *tls, ptls_on_client_hello_parameters_t *params)
{
    int ret;

    // checks the application protocol extension sent by the client, ok if finds the expected one from
    // the server, error if none match or empty
    size_t i, j;
    size_t alpn_len = strlen(quicly_alpn);
    TRACE("stream requested protocol: %s\n", quicly_alpn);
    const ptls_iovec_t *y;
    for (j = 0; j != params->negotiated_protocols.count; ++j) {
        y = params->negotiated_protocols.list + j;
        TRACE(">> protocol check: %s == %s\n", quicly_alpn, y->base);
        if (alpn_len == y->len && memcmp(quicly_alpn, y->base, alpn_len) == 0) {
          ret = ptls_set_negotiated_protocol(tls, (const char *)quicly_alpn, alpn_len);
          return ret;
        }
    }
    return PTLS_ALERT_NO_APPLICATION_PROTOCOL;
}

static const quicly_stream_callbacks_t stream_callbacks = {
    on_destroy,
    on_acked_sent_bytes,
    on_sent_bytes,
    on_stop_sending,
    on_receive,
    on_receive_reset};

static int on_stream_open(quicly_stream_open_t *self, quicly_stream_t *stream)
{
    int ret;

    TRACE("stream opened: %lld\n\n", stream->stream_id);

    if ((ret = quicly_streambuf_create(stream, sizeof(quicly_streambuf_t))) != 0)
        return ret;
    stream->callbacks = &stream_callbacks;

    // callback to go code
    uint64_t connId = get_connection_uuid( stream->conn );
    if( connId != 0 ) {
        goQuiclyOnStreamOpen( connId, uint64_t(stream->stream_id) );
    }

    return 0;
}

static void on_connection_close(quicly_closed_by_remote_t *self, quicly_conn_t *conn, int err, uint64_t frame_type,
                                const char *reason, size_t reason_len)
{
  TRACE("connection was closed err %d for reason: %s\n", err, reason);

  uint64_t connId = get_connection_uuid( conn );
  if( connId != 0 ) {
      delete_connection( connId );
  }
}

static void on_destroy(quicly_stream_t *stream, int err)
{
    TRACE( "stream %lld destroy, err: %d\n\n", stream->stream_id, err );

    if (quicly_sendstate_is_open(&stream->sendstate)) {
        // callback to go code
        uint64_t connId = get_connection_uuid( stream->conn );
        if( connId != 0 ) {
            goQuiclyOnStreamClose( connId, uint64_t(stream->stream_id), err );
        }

        quicly_streambuf_egress_shutdown(stream);
        quicly_streambuf_destroy(stream, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(err));
    }
}

static void on_stop_sending(quicly_stream_t *stream, int err)
{
    TRACE("received STOP_SENDING: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    if (quicly_sendstate_is_open(&stream->sendstate)) {
        // callback to go code
        uint64_t connId = get_connection_uuid( stream->conn );
        if( connId != 0 ) {
            goQuiclyOnStreamClose( connId, uint64_t(stream->stream_id), err );
        }

        quicly_streambuf_egress_shutdown(stream);
    }
}

static void on_receive_reset(quicly_stream_t *stream, int err)
{
    TRACE("received RESET_STREAM: %lld\n", QUICLY_ERROR_GET_ERROR_CODE(err));
    if (quicly_sendstate_is_open(&stream->sendstate)) {
        // callback to go code
        uint64_t connId = get_connection_uuid( stream->conn );
        if( connId != 0 ) {
            goQuiclyOnStreamClose( connId, uint64_t(stream->stream_id), err );
        }

        quicly_streambuf_egress_shutdown(stream);
    }
}

static void on_receive(quicly_stream_t *stream, size_t off, const void *src, size_t len)
{
    TRACE("received PACKET: %lld\n", len);
    if( stream == NULL || stream->data == NULL || stream->conn == NULL || src == NULL || len == 0 ) {
        TRACE("stream was closed\n");
        return;
    }

    /* read input to receive buffer */
    if (quicly_streambuf_ingress_receive(stream, off, src, len) != 0) {
        TRACE("stream has no ingress\n");
        return;
    }

    /* obtain contiguous bytes from the receive buffer */
    ptls_iovec_t input = quicly_streambuf_ingress_get(stream);
    if( input.base == NULL ) {
        TRACE("received empty packet\n");
        return;
    }

    struct iovec vec = {
      .iov_base = (char*)input.base,
      .iov_len  = input.len,
    };

    // callback to go code
    uint64_t connId = get_connection_uuid( stream->conn );
    if( connId != 0 ) {
        TRACE("received PACKET: %lld-%lld, %lld\n", connId, uint64_t(stream->stream_id), len);
        goQuiclyOnStreamReceived( connId, uint64_t(stream->stream_id), &vec);
    }

    /* remove used bytes from receive buffer */
    quicly_streambuf_ingress_shift(stream, input.len);
}

static void on_receive_datagram_frame(quicly_receive_datagram_frame_t *self, quicly_conn_t *conn, ptls_iovec_t payload)
{
    const char* ptr = (const char*)payload.base;
    const size_t len = payload.len;

    const quicly_cid_plaintext_t* cid = quicly_get_master_id(conn);
    if( cid == NULL || payload.base == NULL ) {
        TRACE("Frame dropped\n");
        return;
    }

    TRACE("[%ld] Frame Received: %*s\n", cid->master_id, len, ptr);
}

static void on_acked_sent_bytes(quicly_stream_t *stream, size_t delta)
{
    if( stream != NULL && delta > 0 ) {
       quicly_streambuf_t *sbuf = (quicly_streambuf_t *)stream->data;
       if( sbuf == NULL )
         return;

       quicly_sendbuf_shift(stream, &sbuf->egress, delta);

       TRACE(">> shift -- %lld\n", delta);

       // callback to go code
       uint64_t connId = get_connection_uuid( stream->conn );
       if( connId != 0 ) {
           goQuiclyOnStreamAckedSentBytes( connId, uint64_t(stream->stream_id), delta);
           TRACE(">> ack -- %lld\n", delta);
       }
    }
}

static void on_sent_bytes(quicly_stream_t *stream, size_t off, void *dst, size_t *len, int *wrote_all)
{
    if( stream != NULL && dst != NULL && len != NULL ) {
      quicly_streambuf_t *sbuf = (quicly_streambuf_t *)stream->data;
      if( sbuf == NULL )
        return;

       quicly_sendbuf_emit(stream, &sbuf->egress, off, dst, len, wrote_all);
       TRACE(">> emit -- %lld\n", *len);

       // callback to go code
       uint64_t connId = get_connection_uuid( stream->conn );
       if( connId != 0 ) {
           goQuiclyOnStreamSentBytes( connId, uint64_t(stream->stream_id), (uint64_t)(*len));
       }
    }
}

// ----- Connection ----- //
int QuiclyConnect( const char* _address, int port, size_t conn_id )
{
std::lock_guard<std::mutex> lock(global_lock);

    // Address resolution
    struct in_addr byte_addr;
    byte_addr.s_addr = inet_addr(_address);

    struct sockaddr_in address = {
      .sin_family = AF_INET,
      .sin_port = htons(port),
      .sin_addr = byte_addr
    };

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

    quicly_conn_t* newconn = NULL;

    /* initiate a connection, and open a stream */
    int ret = 0;
    if ((ret = quicly_connect(&newconn, &ctx,
                              _address, (struct sockaddr *)&address, NULL,
                              &next_cid, ptls_iovec_init(NULL, 0),
                              &client_hs_prop,
                              NULL, NULL)) != 0)
    {
        TRACE("quicly_connect failed:%d\n\n", ret);
        return ret;
    }

    // track new connection
    struct quiclygo_conn_entry e = { .uuid=conn_id, .conn=newconn };
    hashmap_set( connections_map, &e );

    // set tracer
    struct _st_quicly_conn_public_t * conn = (struct _st_quicly_conn_public_t *)newconn;
    conn->tracer = qtracer;

    // increment next expected connection id
    next_cid.master_id++;

    return QUICLY_OK;
}

int QuiclyClose( size_t conn_id, int error )
{
std::lock_guard<std::mutex> lock(global_lock);

    // get tracked connection
    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_FAILED;
    }

    delete_connection( conn_id );

    TRACE( "QuiclyClose connID: %lld\n", conn_id);
    int ret = quicly_close(econn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(error), "");

    return ret;
}

int QuiclyProcessMsg( int is_client, const char* _address, int port, char* msg, size_t dgram_len, size_t conn_id )
{
std::lock_guard<std::mutex> lock(global_lock);

    TRACE(">> process\n");
    size_t off = 0, i = 0;

    struct in_addr byte_addr;
    byte_addr.s_addr = inet_addr(_address);

    struct sockaddr_in address = {
      .sin_family = AF_INET,
      .sin_port = htons(port),
      .sin_addr = byte_addr
    };

    // process connection states
    TRACE(">> check connections\n");
    size_t iter = 0;
    void *item = NULL;
    while (hashmap_iter(connections_map, &iter, &item)) {
        const struct quiclygo_conn_entry *e = (const struct quiclygo_conn_entry *)item;
        TRACE( "\n\n>> state: %d %d %p\n\n", i, quicly_get_state(e->conn), e->conn );

        if( quicly_get_state(e->conn) >= QUICLY_STATE_CLOSING ) {
            quicly_close(e->conn, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(0), "");
            hashmap_delete( connections_map, &e );
            iter = 0; // reset iteration by limitation of hashmap
        }
    }
    TRACE("<< check connections\n");

    int err = QUICLY_OK;
    quicly_decoded_packet_t* decoded = NULL;
    quicly_conn_t* conn = NULL;

    /* split UDP datagram into multiple QUIC packets */
    while (off < dgram_len) {
        free(decoded);
        decoded = NULL;
        decoded = (quicly_decoded_packet_t*)malloc( sizeof(quicly_decoded_packet_t) );

        if (quicly_decode_packet(&ctx, decoded, (const uint8_t *)msg, dgram_len, &off) == SIZE_MAX) {
            err = QUICLY_ERROR_DECODE_FAILED;
            break;
        }

        // get destination connection for next packet
        conn = NULL;
        iter = 0;
        while (hashmap_iter(connections_map, &iter, &item)) {
            const struct quiclygo_conn_entry *e = (const struct quiclygo_conn_entry *)item;

            if (quicly_is_destination(e->conn, NULL, (struct sockaddr*)&address, decoded)) {
                TRACE(">> destination (%d)\n", e->uuid);
                conn = e->conn;
                break;
            }
        }

        int ret = 0;
        if( !is_client ) {
            if( conn == NULL ) {
                /* assume that the packet is a new connection */
                TRACE(">> accept\n");

                ret = quicly_accept(&conn, &ctx, NULL, (struct sockaddr*)&address, decoded, NULL, &next_cid, NULL, NULL);
                if( ret != QUICLY_OK ) {
                    TRACE(">> err: %d\n", ret);
                    err = ret;
                    break;
                }

                struct _st_quicly_conn_public_t * conn_pub = (struct _st_quicly_conn_public_t *)(conn);
                conn_pub->tracer = qtracer;

                next_cid.master_id++;
                TRACE(">> accept (%d) ret: %d next: %d\n", i, ret, next_cid.master_id);

                // track connection
                add_connection( conn_id, conn );

            } else {
                quicly_get_first_timeout(conn);

                TRACE(">> serv receive (%d)\n", i);
                ret = quicly_receive(conn, NULL, (struct sockaddr*)&address, decoded);
            }

        } else {
            if( conn == NULL ) {
                err = QUICLY_ERROR_DESTINATION_NOT_FOUND;
                break;
            }
            TRACE(">> client proc (%d)\n", i);

            /* let the current connection handle ingress packets */
            quicly_get_first_timeout(conn);

            ret = quicly_receive(conn, NULL, (struct sockaddr*)&address, decoded);
            TRACE(">> client receive (%d) err: %d\n", i, ret);
        }
        if( ret != 0 ) {
            err = ret;
            break;
        }
    }
    free(decoded);
    decoded = NULL;

    TRACE("<< process (%d)\n", err);
    return err;
}

static uint8_t dgrams_buf[4096 * 1470];

int QuiclyOutgoingMsgQueue( size_t conn_id, struct iovec* dgrams_out, size_t* num_dgrams )
{

std::lock_guard<std::mutex> lock(global_lock);

    quicly_address_t dest, src;

    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    quicly_get_first_timeout(econn);

    int ret = quicly_send(econn, &dest, &src, dgrams_out, num_dgrams, dgrams_buf, 4096 * 1470);
    if( ret != 0 || *num_dgrams > 0 ) {
        TRACE("\n\n>> quicly_send: %d %d\n", ret, *num_dgrams);
    }

    switch (ret) {
    case 0:
      break;
//      case 0: {
//          size_t j;
//          for (j = 0; j != *num_dgrams; ++j) {
//              //send_one(fd, &dest.sa, &dgrams[j]);
//              TRACE("packet %p %d\n\n", dgrams_out[j].iov_base, dgrams_out[j].iov_len);
//          }
//      } break;
    case QUICLY_ERROR_FREE_CONNECTION:
        /* connection has been closed, free, and exit when running as a client */
        quicly_free(econn);
        delete_connection( conn_id );
        return QUICLY_ERROR_NOT_OPEN;

    default:
        TRACE("quicly_send returned %d\n", ret);
        return QUICLY_ERROR_FAILED;
    }

    return QUICLY_OK;
}


// ----- Stream ----- //

int QuiclyOpenStream( size_t conn_id, size_t* stream_id )
{

std::lock_guard<std::mutex> lock(global_lock);

    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    quicly_stream_t *stream = NULL;
    int ret = quicly_open_stream(econn, &stream, 0);
    if( ret == QUICLY_OK ) {
        *stream_id = stream->stream_id;
    }
    return ret;
}

int QuiclyCloseStream( size_t conn_id, size_t stream_id, int err )
{

std::lock_guard<std::mutex> lock(global_lock);

    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    quicly_stream_t *stream = quicly_get_stream(econn, stream_id);
    if( stream == NULL ) {
        return QUICLY_ERROR_STREAM_NOT_FOUND;
    }

    if (quicly_sendstate_is_open(&stream->sendstate)) {
        quicly_reset_stream( stream, QUICLY_ERROR_FROM_APPLICATION_ERROR_CODE(err) );

        // callback to go code
        goQuiclyOnStreamClose( conn_id, uint64_t(stream->stream_id), err );
    }

    return QUICLY_OK;
}

int QuiclyWriteStream( size_t conn_id, size_t stream_id, char* msg, size_t dgram_len )
{
    if( dgram_len <= 0 )
        return QUICLY_OK;

std::lock_guard<std::mutex> lock(global_lock);

    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    quicly_stream_t *stream = quicly_get_stream(econn, stream_id);
    if( stream == NULL ) {
        TRACE("write: error not found\n");
        return QUICLY_ERROR_STREAM_NOT_FOUND;
    }

    if (quicly_sendstate_is_open(&stream->sendstate) ) {
        int res = quicly_streambuf_egress_write(stream, msg, dgram_len);
        TRACE("write: %d - %d\n", res, dgram_len );
        return res;
    }

    return QUICLY_OK;
}

int QuiclySendDatagram( size_t conn_id, char* msg, size_t dgram_len )
{
    if( dgram_len <= 0 )
        return QUICLY_OK;

std::lock_guard<std::mutex> lock(global_lock);

    quicly_conn_t* econn = get_connection( conn_id );
    if( econn == NULL ) {
        return QUICLY_ERROR_DESTINATION_NOT_FOUND;
    }

    ptls_iovec_t datagram = ptls_iovec_init(msg, dgram_len);
    quicly_send_datagram_frames(econn, &datagram, 1);

    return QUICLY_OK;
}

static void print_ranges( const char* prefix, quicly_ranges_t* ranges_st ) {
  if( ranges_st == NULL )
    return;

  TRACE("%s: [", prefix);
  for( int i=0; i<ranges_st->num_ranges; i++ ) {
    TRACE("{%d:%d},", ranges_st->ranges[i].start, ranges_st->ranges[i].end);
  }
  TRACE("]\n");
}

// ----- Scheduler ----- //
/**
 * See doc-comment of `st_quicly_default_scheduler_state_t` to understand the logic.
 */
static int quiclygo_stream_scheduler_can_send(quicly_stream_scheduler_t *self, quicly_conn_t *conn, int conn_is_saturated)
{
    struct st_quicly_default_scheduler_state_t *sched = &((struct _st_quicly_conn_public_t *)conn)->_default_scheduler;

    if (!conn_is_saturated) {
        /* not saturated */
        quicly_linklist_insert_list(&sched->active, &sched->blocked);
    //} else {
        /* The code below is disabled, because H2O's scheduler doesn't allow you to "walk" the priority tree without actually
         * running the round robin, and we want quicly's default to behave like H2O so that we can catch errors.  The downside is
         * that there'd be at most one spurious call of `quicly_send` when the connection is saturated, but that should be fine.
         */
        if (0) {
            /* Saturated. Lazily move such streams to the "blocked" list, at the same time checking if anything can be sent. */
//            while (quicly_linklist_is_linked(&sched->active)) {
//                quicly_stream_t *stream =
//                    (void *)((char *)sched->active.next - offsetof(quicly_stream_t, _send_aux.pending_link.default_scheduler));
//                if (quicly_stream_can_send(stream, 0))
//                    return 1;
//                quicly_linklist_unlink(&stream->_send_aux.pending_link.default_scheduler);
//                quicly_linklist_insert(sched->blocked.prev, &stream->_send_aux.pending_link.default_scheduler);
//            }
        }
    }

    int can_send = quicly_linklist_is_linked(&sched->active);
    return can_send;
}

static void link_stream(struct st_quicly_default_scheduler_state_t *sched, quicly_stream_t *stream, int conn_is_blocked)
{
    if (!quicly_linklist_is_linked(&stream->_send_aux.pending_link.default_scheduler)) {
        quicly_linklist_t *slot = &sched->active;
        if (conn_is_blocked && !quicly_stream_can_send(stream, 0))
            slot = &sched->blocked;
        quicly_linklist_insert(slot->prev, &stream->_send_aux.pending_link.default_scheduler);
    }
}

/**
 * See doc-comment of `st_quicly_default_scheduler_state_t` to understand the logic.
 */
static int quiclygo_stream_scheduler_do_send(quicly_stream_scheduler_t *self, quicly_conn_t *conn, quicly_send_context_t *s)
{
    struct st_quicly_default_scheduler_state_t *sched = &((struct _st_quicly_conn_public_t *)conn)->_default_scheduler;
    int conn_is_blocked = quicly_is_blocked(conn), ret = 0;

    if (!conn_is_blocked)
        quicly_linklist_insert_list(&sched->active, &sched->blocked);
    else
        TRACE( "Connection is blocked." );

    while (quicly_can_send_data((quicly_conn_t *)conn, s) && quicly_linklist_is_linked(&sched->active)) {
        /* detach the first active stream */
        quicly_stream_t *stream =
            (quicly_stream_t *)((char *)sched->active.next - offsetof(quicly_stream_t, _send_aux.pending_link.default_scheduler));
        quicly_linklist_unlink(&stream->_send_aux.pending_link.default_scheduler);
        /* relink the stream to the blocked list if necessary */
        if (conn_is_blocked && !quicly_stream_can_send(stream, 0)) {
            quicly_linklist_insert(sched->blocked.prev, &stream->_send_aux.pending_link.default_scheduler);
            continue;
        }
        /* send! */
        if ((ret = quicly_send_stream(stream, s)) != 0) {
            /* FIXME Stop quicly_send_stream emitting SENDBUF_FULL (happens when CWND is congested). Otherwise, we need to make
             * adjustments to the scheduler after popping a stream */
            if (ret == QUICLY_ERROR_SENDBUF_FULL) {
                TRACE( "Sendbuffer is full." );
                assert(quicly_stream_can_send(stream, 1));
                link_stream(sched, stream, conn_is_blocked);
            }
            break;
        }
        /* reschedule */
        conn_is_blocked = quicly_is_blocked(conn);
        if( conn_is_blocked )
            TRACE( "Connection is blocked." );
        if (quicly_stream_can_send(stream, 1))
            link_stream(sched, stream, conn_is_blocked);
    }

    return ret;
}

/**
 * See doc-comment of `st_quicly_default_scheduler_state_t` to understand the logic.
 */
static int quiclygo_stream_scheduler_update_state(quicly_stream_scheduler_t *self, quicly_stream_t *stream)
{
    struct st_quicly_default_scheduler_state_t *sched = &((struct _st_quicly_conn_public_t *)stream->conn)->_default_scheduler;

    if (quicly_stream_can_send(stream, 1)) {
        TRACE( "Stream can send." );
        /* activate if not */
        link_stream(sched, stream, quicly_is_blocked(stream->conn));
    } else {
        TRACE( "Stream cannot send." );
        /* deactivate if active */
        if (quicly_linklist_is_linked(&stream->_send_aux.pending_link.default_scheduler))
            quicly_linklist_unlink(&stream->_send_aux.pending_link.default_scheduler);
    }

    return 0;
}

#endif
