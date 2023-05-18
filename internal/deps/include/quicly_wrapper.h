
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

struct iovec;

extern int   QuiclyInitializeEngine();

extern int   QuiclyCloseEngine();

extern int   QuiclyProcessMsg( int is_client, const char* address, int port, char* msg, size_t dgram_len, size_t* id );

extern int   QuiclyConnect( const char* address, int port, size_t* id );

extern int   QuiclyOpenStream( size_t conn_id, size_t* stream_id );

extern int   QuiclyCloseStream( size_t conn_id, size_t stream_id, int error );

extern int   QuiclyClose( size_t conn_id, int error );

extern int   QuiclyOutgoingMsgQueue( size_t id, struct iovec* dgram, size_t* num_dgrams );

extern int   QuiclyWriteStream( size_t conn_id, size_t stream_id, char* msg, size_t dgram_len );

extern void  goQuiclyOnStreamOpen(uint64_t conn_id, uint64_t stream_id);

extern void  goQuiclyOnStreamClose(uint64_t conn_id, uint64_t stream_id, int error);

extern void  goQuiclyOnStreamReceived(uint64_t conn_id, uint64_t stream_id, struct iovec* packet);

#endif
