package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import (
	"fmt"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"sync"
	"unsafe"
)

var connectionsRegistry map[uint64]types.Session
var callbackLock sync.RWMutex

func ResetRegistry() {
	callbackLock.Lock()
	defer callbackLock.Unlock()
	connectionsRegistry = make(map[uint64]types.Session)
}

func RegisterConnection(s types.Session, id uint64) {
	if s == nil {
		return
	}
	callbackLock.Lock()
	defer callbackLock.Unlock()
	if connectionsRegistry[id] != s {
		fmt.Printf("registered connection id: %d index: %d\n", s.ID(), id)
		connectionsRegistry[id] = s
	}
}

func GetConnection(id uint64) (types.Session, bool) {
	callbackLock.RLock()
	defer callbackLock.RUnlock()
	s, ok := connectionsRegistry[id]
	return s, ok
}

func RemoveConnection(id uint64) {
	callbackLock.Lock()
	defer callbackLock.Unlock()
	delete(connectionsRegistry, id)
}

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	fmt.Printf("open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	callbackLock.RLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	callbackLock.RUnlock()

	if !ok {
		return
	}

	conn.OnStreamOpen(uint64(stream_id))
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	fmt.Printf("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))
	callbackLock.RLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	callbackLock.RUnlock()

	if !ok {
		return
	}

	conn.OnStreamClose(uint64(stream_id), int(error))
}

//export goQuiclyOnStreamReceived
func goQuiclyOnStreamReceived(conn_id C.uint64_t, stream_id C.uint64_t, data *C.struct_iovec) {
	callbackLock.RLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	callbackLock.RUnlock()

	if !ok {
		fmt.Printf("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		fmt.Printf("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	vIn := Iovec{}
	vIn.ref4b778f8 = data
	vIn.Deref()

	buf := IovecToBytes(vIn)
	st.OnReceived(buf, int(vIn.Iov_len))
}

func IovecToBytes(data Iovec) []byte {
	return unsafe.Slice((*byte)(data.Iov_base), int(data.Iov_len))
}
