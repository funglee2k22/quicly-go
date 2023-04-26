package quiclylib

const (
	// QUICLY_OK No error from connection library
	QUICLY_OK = 0
	// QUICLY_ERROR_NOTINITILIZED Could not initialize connection library
	QUICLY_ERROR_NOTINITILIZED = 1
	// QUICLY_ERROR_ALREADY_INIT Cannot re-initialize connection library
	QUICLY_ERROR_ALREADY_INIT = 2
	// QUICLY_ERROR_FAILED Failure in connection library
	QUICLY_ERROR_FAILED = 3
	// QUICLY_ERROR_NOT_OPEN Requested state is not available
	QUICLY_ERROR_NOT_OPEN = 4
)
