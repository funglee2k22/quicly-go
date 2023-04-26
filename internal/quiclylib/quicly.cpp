
// definition for local dev
// #ifndef WIN32
// #define WIN32
// #endif

#ifdef WIN32

extern "C" {
    #include "quicly_wrapper.h"
    #include "stdio.h"
    #include "stdarg.h"
    #include "stdlib.h"
}

int InitializeQuiclyEngine() {
  return QUICLY_OK;
}

int CloseQuiclyEngine() {
  return QUICLY_OK;
}

#endif
