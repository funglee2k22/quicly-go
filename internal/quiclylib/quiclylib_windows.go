//go:build windows && cgo

package quiclylib

//#cgo windows CPPFLAGS: -DWIN32 -D_WIN32_WINNT=0x0A00 -I${SRCDIR}/include/ -I${SRCDIR}/deps/
//#cgo windows,amd64 LDFLAGS: -L ${SRCDIR}/lib/VC/static ${SRCDIR}/lib/libquicly.a ${SRCDIR}/lib/libpicotls.a -llibcrypto64MD -lm -lmswsock -lws2_32
//#include "quicly_wrapper.h"
import "C"

func InitializeQuiclyEngine() int {
	response := int(C.InitializeQuiclyEngine())
	return response
}

func CloseQuiclyEngine() int {
	return int(C.CloseQuiclyEngine())
}
