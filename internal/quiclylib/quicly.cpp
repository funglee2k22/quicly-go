
#ifdef WIN32

extern "C" {
    #include "quicly_wrapper.h"
    #include "stdio.h"
    #include "stdarg.h"
    #include "stdlib.h"
}

#define SERVER_CERT  "server_cert.pem"
#define SERVER_KEY   "server_key.pem"

/**
 * the QUIC context
 */
static quicly_context_t ctx;
/**
 * CID seed
 */
static quicly_cid_plaintext_t next_cid;

static quicly_conn_t *conns_table[256] = {};

// ----- Startup ----- //

int QuiclyInitializeEngine() {
#ifdef WIN32
  printf("starting\n");
  WSADATA wsaData;
  WORD wVersionRequested = MAKEWORD(2, 2);
  int err = WSAStartup(wVersionRequested, &wsaData);
  if( err != 0 ) {
      printf("WSAStartup failed with error: %d\n", err);
      return QUICLY_ERROR_FAILED;
  }
#endif

  ptls_openssl_sign_certificate_t sign_certificate;
  ptls_context_t tlsctx = {
    .random_bytes = ptls_openssl_random_bytes,
    .get_time = &ptls_get_time,
    .key_exchanges = ptls_openssl_key_exchanges,
    .cipher_suites = ptls_openssl_cipher_suites,
  };
  quicly_stream_open_t stream_open = {};

//  /* setup quic context */
  ctx = quicly_spec_context;
  ctx.tls = &tlsctx;
  quicly_amend_ptls_context(ctx.tls);
  ctx.stream_open = &stream_open;

  // load certificate
  int ret;
  if ((ret = ptls_load_certificates(&tlsctx, SERVER_CERT)) != 0) {
      fprintf(stderr, "failed to load certificates from file[%d]: %s\n", ret, ERR_error_string(ret, NULL));
      return QUICLY_ERROR_FAILED;
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
  ptls_openssl_init_sign_certificate(&sign_certificate, pkey);
  EVP_PKEY_free(pkey);
  tlsctx.sign_certificate = &sign_certificate.super;

  // check consistency
  if ((tlsctx.certificates.count != 0) != (tlsctx.sign_certificate != NULL)) {
    return QUICLY_ERROR_FAILED;
  }

  return QUICLY_OK;
}

int QuiclyCloseEngine() {
#ifdef WIN32
  printf("closing\n");
  WSACleanup();
#endif

  return QUICLY_OK;
}

// ----- Connection ----- //
int QuiclyProcessMsg( int is_client, unsigned short family, unsigned short port,
                      void* _addr, void* _msg, size_t dgram_len, unsigned long long* id)
{
    size_t off = 0, i = 0;

    char* addr = (char*)_addr;
    char* msg  = (char*)_msg;

    struct in_addr byteAddr;
    byteAddr.s_addr = inet_addr(addr);

    struct sockaddr_in address{
      .sin_family = family,
      .sin_port = htons(port),
      .sin_addr = byteAddr
    };

    /* split UDP datagram into multiple QUIC packets */
    while (off < dgram_len) {
        quicly_decoded_packet_t decoded;
        if (quicly_decode_packet(&ctx, &decoded, (const uint8_t *)msg, dgram_len, &off) == SIZE_MAX)
            return QUICLY_ERROR_FAILED;

        /* find the corresponding connection */
        for (i = 0; i < 256 && conns_table[i] != NULL; ++i)
            if (quicly_is_destination(conns_table[i], NULL, (struct sockaddr*)&address, &decoded))
                break;
        if (conns_table[i] != NULL) {
            /* let the current connection handle ingress packets */
            quicly_receive(conns_table[i], NULL, (struct sockaddr*)&address, &decoded);
        } else if (!is_client) {
            if( id != NULL ) {
              *id = i;
            }
            /* assume that the packet is a new connection */
            quicly_accept(conns_table + i, &ctx, NULL, (struct sockaddr*)&address, &decoded, NULL, &next_cid, NULL, NULL);
        }
    }

    return QUICLY_OK;
}

int QuiclySendMsg( unsigned long long id, packetbuff* dgrams, unsigned long long* num_dgrams )
{
    quicly_address_t dest, src;
    uint8_t dgrams_buf[PTLS_ELEMENTSOF(dgrams) * ctx.transport_params.max_udp_payload_size];

    int ret = quicly_send(conns_table[id], &dest, &src, dgrams, num_dgrams, dgrams_buf, sizeof(dgrams_buf));
    switch (ret) {
//    case 0: {
//        size_t j;
//        for (j = 0; j != *num_dgrams; ++j) {
//            //send_one(fd, &dest.sa, &dgrams[j]);
//        }
//    } break;
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

int CopyPacket( packetbuff* dgram, void* dst, unsigned long long* dst_size )
{
    if( dgram == NULL || dst == NULL || dst_size == NULL )
        return QUICLY_ERROR_FAILED;

    if( dgram->iov_len > *dst_size )
        return QUICLY_ERROR_FAILED;

    char* dst_ptr = (char*)dst;
    memcpy( dst, dgram->iov_base, dgram->iov_len );
    *dst_size = dgram->iov_len;

    return QUICLY_OK;
}

int GetPacketLen( packetbuff* dgram, unsigned long long* dst_size )
{
}

// ----- Stream ----- //



#endif
