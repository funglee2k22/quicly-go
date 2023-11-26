
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
  QUICLY_ERROR_CERT_LOAD_FAILED = 4,         //!< Certificate load failed
  QUICLY_ERROR_DECODE_FAILED = 5,         //!< Packet decode failed
  QUICLY_ERROR_DESTINATION_NOT_FOUND = 6,         //!< Connection was not found
  QUICLY_ERROR_NOT_OPEN = 7,       //!< Connection is not open so no state available
  QUICLY_ERROR_STREAM_NOT_FOUND = 8,         //!< Stream was not found in connection
};

struct iovec;

// API
extern int   QuiclyInitializeEngine( const char* alpn, const char* certificate_file, const char* key_file, const uint64_t idle_timeout_ms );

extern int   QuiclyCloseEngine();

extern int   QuiclyProcessMsg( int is_client, const char* address, int port, char* msg, size_t dgram_len, size_t* id );

extern int   QuiclyConnect( const char* address, int port, size_t* id );

extern int   QuiclyOpenStream( size_t conn_id, size_t* stream_id );

extern int   QuiclyCloseStream( size_t conn_id, size_t stream_id, int error );

extern int   QuiclyClose( size_t conn_id, int error );

extern int   QuiclyOutgoingMsgQueue( size_t id, struct iovec* dgram, size_t* num_dgrams );

extern int   QuiclyWriteStream( size_t conn_id, size_t stream_id, char* msg, size_t dgram_len );

// Callbacks
extern void  goQuiclyOnStreamOpen(uint64_t conn_id, uint64_t stream_id);

extern void  goQuiclyOnStreamClose(uint64_t conn_id, uint64_t stream_id, int error);

extern void  goQuiclyOnStreamReceived(uint64_t conn_id, uint64_t stream_id, struct iovec* packet);

#endif
