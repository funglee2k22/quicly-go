
#ifdef WIN32

extern "C" {
    #include "quicly_wrapper.h"
    #include "stdio.h"
    #include "stdarg.h"
    #include "stdlib.h"
}

/**
 * the QUIC context
 */
static quicly_context_t ctx;
/**
 * CID seed
 */
static quicly_cid_plaintext_t next_cid;

// ----- Startup ----- //

int InitializeQuiclyEngine() {
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


  return QUICLY_OK;
}

int CloseQuiclyEngine() {


#ifdef WIN32
  printf("closing\n");
  WSACleanup();
#endif

  return QUICLY_OK;
}

// ----- Connection ----- //


// ----- Stream ----- //



#endif
