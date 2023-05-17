
#ifndef QUICLY_WRAPPER
#define QUICLY_WRAPPER

#include "stdio.h"
#include "stdarg.h"
#include "stdlib.h"
#include "stdint.h"

enum {
  QUICLY_OK = 0,  //!< No issue
  QUICLY_ERROR_NOTINITILIZED = 1,  //!< InitializeWinDivertEngine was not called previously
  QUICLY_ERROR_ALREADY_INIT  = 2,  //!< InitializeWinDivertEngine called again before CloseWinDivertEngine
  QUICLY_ERROR_FAILED = 3,         //!< Operation failed
  QUICLY_ERROR_NOT_OPEN = 4,       //!< Connection is not open so no state available
};

extern int  QuiclyInitializeEngine();
extern int  QuiclyCloseEngine();

extern int  QuiclyClientProcessMsg( const char* address, int port, char* msg, size_t dgram_len );

extern int  QuiclyServerProcessMsg( const char* address, int port, char* msg, size_t dgram_len, size_t* newconn_id );

extern int  QuiclySendMsg( size_t id, struct iovec* dgram, size_t* num_dgrams );

void  goQuiclyOnStreamOpen(uint64_t conn_id, uint64_t stream_id);

void  goQuiclyOnStreamClose(uint64_t conn_id, uint64_t stream_id, int error);

#endif
