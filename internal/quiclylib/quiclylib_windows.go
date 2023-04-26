//go:build windows && cgo

package quiclylib

//#cgo windows CPPFLAGS: -DWIN32 -D_WIN32_WINNT=0x0600 -I ${SRCDIR}/include/
//#cgo windows,amd64 LDFLAGS: ${SRCDIR}/lib/libquicly.a
//#include "quicly_wrapper.h"
import "C"

func InitializeWinDivertEngine() int {
	response := int(C.InitializeQuiclyEngine())
	return response
}

func CloseWinDivertEngine() int {
	return int(C.CloseQuiclyEngine())
}
