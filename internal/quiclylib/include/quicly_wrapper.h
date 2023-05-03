
#ifndef QUICLY_WRAPPER
#define QUICLY_WRAPPER

#ifdef WIN32

// Including SDKDDKVer.h defines the highest available Windows platform.
// If you wish to build your application for a previous Windows platform, include WinSDKVer.h and
// set the _WIN32_WINNT macro to the platform you wish to support before including SDKDDKVer.h.
#include <SDKDDKVer.h>

#define WIN32_LEAN_AND_MEAN // Exclude rarely-used stuff from Windows headers
// Windows Header Files:
#include <windows.h>

#endif // WIN32

#include "quicly.h"

enum {
  QUICLY_OK = 0,  //!< No issue
  QUICLY_ERROR_NOTINITILIZED = 1,  //!< InitializeWinDivertEngine was not called previously
  QUICLY_ERROR_ALREADY_INIT  = 2,  //!< InitializeWinDivertEngine called again before CloseWinDivertEngine
  QUICLY_ERROR_FAILED = 3,         //!< Operation failed
  QUICLY_ERROR_NOT_OPEN = 4,       //!< Connection is not open so no state available
};

extern int  InitializeQuiclyEngine();
extern int  CloseQuiclyEngine();

#endif
