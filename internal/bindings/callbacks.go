package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import (
	"fmt"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"sync"
)

var connectionsRegistry map[uint64]types.Session
var callbackLock sync.Mutex

func QuiclyResetRegistry() {
	callbackLock.Lock()
	defer callbackLock.Unlock()
	connectionsRegistry = make(map[uint64]types.Session)
}

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	fmt.Printf("open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	callbackLock.Lock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	callbackLock.Unlock()

	if !ok {
		return
	}

	conn.OnStreamOpen(uint64(stream_id))
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	fmt.Printf("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	callbackLock.Lock()
	conn, ok := connectionsRegistry[uint64(conn_id)]
	callbackLock.Unlock()

	if !ok {
		return
	}

	conn.OnStreamClose(uint64(stream_id), int(error))
}
