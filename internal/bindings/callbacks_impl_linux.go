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
	did_lock := callbackLock.TryLock()
	connectionsRegistry = make(map[uint64]types.Session)
	if did_lock {
		callbackLock.Unlock()
	}
}

func RegisterConnection(s types.Session, id uint64) {
	if s == nil {
		return
	}
	did_lock := callbackLock.TryLock()
	if connectionsRegistry[id] != s {
		fmt.Printf("registered connection id: %d index: %d\n", s.ID(), id)
		connectionsRegistry[id] = s
	}
	if did_lock {
		callbackLock.Unlock()
	}
}

func GetConnection(id uint64) (types.Session, bool) {
	did_lock := callbackLock.TryLock()
	s, ok := connectionsRegistry[id]
	if did_lock {
		callbackLock.Unlock()
	}
	return s, ok
}

func RemoveConnection(id uint64) {
	did_lock := callbackLock.TryLock()
	fmt.Printf("removed connection id: %d index: %d\n", connectionsRegistry[id].ID(), id)
	delete(connectionsRegistry, id)
	if did_lock {
		callbackLock.Unlock()
	}
}

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	fmt.Printf("open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	did_lock_cb := callbackLock.TryLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	if did_lock_cb {
		callbackLock.Unlock()
	}

	if !ok {
		fmt.Printf("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	did_lock := mtx_bindings.TryLock()
	conn.OnStreamOpen(uint64(stream_id))
	if did_lock {
		mtx_bindings.Unlock()
	}
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	fmt.Printf("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	did_lock_cb := callbackLock.TryLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	if did_lock_cb {
		callbackLock.Unlock()
	}

	fmt.Printf("close stream 2\n")
	if !ok {
		return
	}
	fmt.Printf("close stream 3\n")

	did_lock := mtx_bindings.TryLock()
	conn.OnStreamClose(uint64(stream_id), int(error))
	if did_lock {
		mtx_bindings.Unlock()
	}
	fmt.Printf("close stream 4\n")
}

//export goQuiclyOnStreamReceived
func goQuiclyOnStreamReceived(conn_id C.uint64_t, stream_id C.uint64_t, data *C.struct_iovec) {
	//fmt.Printf("received stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	did_lock_cb := callbackLock.TryLock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	if did_lock_cb {
		callbackLock.Unlock()
	}

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

	did_lock := mtx_bindings.TryLock()
	st.OnReceived(buf, int(vIn.Iov_len))
	if did_lock {
		mtx_bindings.Unlock()
	}
}

func IovecToBytes(data Iovec) []byte {
	return unsafe.Slice((*byte)(data.Iov_base), int(data.Iov_len))
}
