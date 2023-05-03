
#ifdef WIN32

extern "C" {
    #include "quicly_wrapper.h"
    #include "stdio.h"
    #include "stdarg.h"
    #include "stdlib.h"
}

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

  return QUICLY_OK;
}

int CloseQuiclyEngine() {
#ifdef WIN32
  printf("closing\n");
  WSACleanup();
#endif

  return QUICLY_OK;
}

#endif
