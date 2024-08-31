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
var mtx_registry sync.Mutex
var TracingOn = false

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

	for _, st := range connectionsRegistry {
		if s.ID() == st.ID() {
			logTraceMessage("skipped add connection id: %d found: %d (%v)\n", id, st.ID(), &s)
			return
		}
	}

	if connectionsRegistry[id] == nil || connectionsRegistry[id].ID() != s.ID() {
		logTraceMessage("added connection id: %d index: %d (%v)\n", s.ID(), id, &s)
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

	if s, ok := connectionsRegistry[id]; ok {
		logTraceMessage("removed connection id: %d index: %d (%v)\n", s.ID(), id, &s)
		delete(connectionsRegistry, id)
	}
}

func logTraceMessage(format string, args ...interface{}) {
	if TracingOn {
		fmt.Printf(format, args...)
	}
}

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	logTraceMessage(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	logTraceMessage(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))
	conn, ok := connectionsRegistry[uint64(conn_id)]
	if !ok {
		logTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	conn.OnStreamOpen(uint64(stream_id))
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	logTraceMessage("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	if !ok {
		return
	}

	conn.OnStreamClose(uint64(stream_id), int(error))
}

//export goQuiclyOnStreamReceived
func goQuiclyOnStreamReceived(conn_id C.uint64_t, stream_id C.uint64_t, data *C.struct_iovec) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	if !ok {
		logTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		logTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	vIn := Iovec{}
	vIn.ref4b778f8 = data
	vIn.Deref()

	buf := IovecToBytes(vIn)

	st.OnReceived(buf, int(vIn.Iov_len))
}

//export goQuiclyOnStreamSentBytes
func goQuiclyOnStreamSentBytes(conn_id C.uint64_t, stream_id C.uint64_t, sentBytes C.uint64_t) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	if !ok {
		logTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		logTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st.OnSentBytes(uint64(sentBytes))
}

//export goQuiclyOnStreamAckedSentBytes
func goQuiclyOnStreamAckedSentBytes(conn_id C.uint64_t, stream_id C.uint64_t, ackedSentBytes C.uint64_t) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	conn, ok := connectionsRegistry[uint64(conn_id)]

	if !ok {
		logTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		logTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st.OnAckedSentBytes(uint64(ackedSentBytes))
}
