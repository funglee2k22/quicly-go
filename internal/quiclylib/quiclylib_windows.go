//go:build windows && cgo

package quiclylib

//#cgo windows CPPFLAGS: -DWIN32 -D_WIN32_WINNT=0x0A00 -I${SRCDIR}/include/ -I${SRCDIR}/deps/
//#cgo windows,amd64 LDFLAGS: ${SRCDIR}/lib/libquicly.a ${SRCDIR}/lib/libpicotls.a ${SRCDIR}/lib/libcrypto.a ${SRCDIR}/lib/libssl.a -lm -lmswsock -lws2_32
//#include "quicly_wrapper.h"
import "C"
import (
	"net"
	"unsafe"
)

func QuiclyInitializeEngine() int {
	response := int(C.QuiclyInitializeEngine())
	return response
}

func QuiclyCloseEngine() int {
	return int(C.QuiclyCloseEngine())
}

func QuiclyProcessMsg(isClient bool, addr *net.UDPAddr, msg []byte) int {
	is_client := 0
	if isClient {
		is_client = 1
	}

	port := addr.Port
	strAddr := []byte(addr.IP.To4().String())

	return int(C.QuiclyProcessMsg(C.int(is_client), C.AF_INET, C.ushort(port), unsafe.Pointer(&strAddr[0]), unsafe.Pointer(&msg[0]), C.ulonglong(len(msg))))
}
