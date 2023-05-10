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

func QuiclyProcessMsg(isClient bool, addr *net.UDPAddr, msg []byte, id *uint64) int {
	is_client := 0
	if isClient {
		is_client = 1
	}

	port := addr.Port
	strAddr := []byte(addr.IP.To4().String())
	ptrid := C.ulonglong(*id)
	defer func() {
		*id = uint64(ptrid)
	}()

	return int(C.QuiclyProcessMsg(C.int(is_client), C.AF_INET, C.ushort(port),
		unsafe.Pointer(&strAddr[0]), unsafe.Pointer(&msg[0]), C.ulonglong(len(msg)),
		&ptrid))
}

func QuiclySendMsg(id uint64) (int, [][]byte) {

	const psize = unsafe.Sizeof(C.packetbuff{})

	packetsLen := C.ulonglong(10)
	packets := (*C.packetbuff)(C.malloc(packetsLen * C.ulonglong(psize)))

	defer func() {
		C.free(unsafe.Pointer(packets))
	}()

	result := int(C.QuiclySendMsg(C.ulonglong(id), packets, &packetsLen))
	if result != QUICLY_OK || packetsLen == 0 {
		return result, nil
	}

	var packetsList = make([][]byte, 0, uint64(packetsLen))

	currPtr := unsafe.Pointer(packets)
	for i := uint64(0); i < uint64(packetsLen); i++ {
		buffsize := C.ulonglong(4096)
		C.GetPacketLen((*C.packetbuff)(currPtr), &buffsize)

		var packetBuff = make([]byte, 4096)

		ret := C.CopyPacket((*C.packetbuff)(currPtr), unsafe.Pointer(&packetBuff[0]), &buffsize)

		currPtr = unsafe.Add(currPtr, 1)

		if ret != QUICLY_OK {
			continue
		}

		packetsList = append(packetsList, packetBuff)
	}

	return QUICLY_OK, packetsList
}
