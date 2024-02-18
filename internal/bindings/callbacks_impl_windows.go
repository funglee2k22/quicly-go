package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import (
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"sync"
)

var connectionsRegistry map[uint64]types.Session
var mtx_registry sync.Mutex

func ResetRegistry() {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	connectionsRegistry = make(map[uint64]types.Session)
}

func RegisterConnection(s types.Session, id uint64) {
	if s == nil {
		return
	}
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	if connectionsRegistry[id] != s {
		//fmt.Printf("registered connection id: %d index: %d\n", s.ID(), id)
		connectionsRegistry[id] = s
	}
}

func GetConnection(id uint64) (types.Session, bool) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	s, ok := connectionsRegistry[id]
	return s, ok
}

func RemoveConnection(id uint64) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	if _, ok := connectionsRegistry[id]; ok {
		//fmt.Printf("removed connection id: %d index: %d\n", connectionsRegistry[id].ID(), id)
		delete(connectionsRegistry, id)
	}
}

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	//fmt.Printf(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	//fmt.Printf(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))
	conn, ok := connectionsRegistry[uint64(conn_id)]
	if !ok {
		//fmt.Printf("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	conn.OnStreamOpen(uint64(stream_id))
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	//fmt.Printf("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	//fmt.Printf("close stream 2\n")
	if !ok {
		return
	}
	//fmt.Printf("close stream 3\n")

	conn.OnStreamClose(uint64(stream_id), int(error))
	//fmt.Printf("close stream 4\n")
}

//export goQuiclyOnStreamReceived
func goQuiclyOnStreamReceived(conn_id C.uint64_t, stream_id C.uint64_t, data *C.struct_iovec) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	if !ok {
		//fmt.Printf("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		//fmt.Printf("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	vIn := Iovec{}
	vIn.ref4b778f8 = data
	vIn.Deref()

	buf := IovecToBytes(vIn)

	st.OnReceived(buf, int(vIn.Iov_len))
}

func IovecToBytes(data Iovec) []byte {
	tmp := append([]byte{}, data.Iov_base...)
	return tmp
}
