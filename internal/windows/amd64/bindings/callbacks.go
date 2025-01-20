package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import (
	"github.com/Project-Faster/quicly-go/internal"
)

//export goQuiclyOnStreamOpen
func goQuiclyOnStreamOpen(conn_id C.uint64_t, stream_id C.uint64_t) {
	internal.LogTraceMessage(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	internal.LogTraceMessage(">> open stream: %d %d\n", uint64(conn_id), uint64(stream_id))
	conn, ok := internal.GetConnection(uint64(conn_id))
	if !ok {
		internal.LogTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	conn.OnStreamOpen(uint64(stream_id))
}

//export goQuiclyOnStreamClose
func goQuiclyOnStreamClose(conn_id C.uint64_t, stream_id C.uint64_t, error C.int) {
	internal.LogTraceMessage("close stream: %d %d\n", uint64(conn_id), uint64(stream_id))

	conn, ok := internal.GetConnection(uint64(conn_id))

	if !ok {
		return
	}

	conn.OnStreamClose(uint64(stream_id), int(error))
}

//export goQuiclyOnStreamReceived
func goQuiclyOnStreamReceived(conn_id C.uint64_t, stream_id C.uint64_t, data *C.struct_iovec) {
	conn, ok := internal.GetConnection(uint64(conn_id))

	if !ok {
		internal.LogTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		internal.LogTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
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
	conn, ok := internal.GetConnection(uint64(conn_id))

	if !ok {
		internal.LogTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		internal.LogTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st.OnSentBytes(uint64(sentBytes))
}

//export goQuiclyOnStreamAckedSentBytes
func goQuiclyOnStreamAckedSentBytes(conn_id C.uint64_t, stream_id C.uint64_t, ackedSentBytes C.uint64_t) {
	conn, ok := internal.GetConnection(uint64(conn_id))

	if !ok {
		internal.LogTraceMessage("could not find connection: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st := conn.GetStream(uint64(stream_id))
	if st == nil {
		internal.LogTraceMessage("could not find stream: %d %d\n", uint64(conn_id), uint64(stream_id))
		return
	}

	st.OnAckedSentBytes(uint64(ackedSentBytes))
}
