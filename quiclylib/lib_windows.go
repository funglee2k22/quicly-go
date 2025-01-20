package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal"
	"github.com/Project-Faster/quicly-go/internal/windows/amd64/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ types.ServerSession = &QServerSession{}
var _ types.ClientSession = &QClientSession{}
var _ types.Stream = &QStream{}

const (
	READ_SIZE         = 32 * 1024
	SMALL_BUFFER_SIZE = 4 * 1024

	WRITE_TIMEOUT = 500 * time.Millisecond
)

type Size_t = bindings.Size_t
type Iovec = bindings.Iovec

type timeoutErrorType struct{}

func (e *timeoutErrorType) Error() string {
	return "stream timed-out"
}

func (e *timeoutErrorType) Timeout() bool {
	return true
}

func (e *timeoutErrorType) Temporary() bool {
	return true
}

var timeoutError = &timeoutErrorType{}

var _ net.Error = timeoutError

func QuiclyInitializeEngine(options types.Options) int {
	internal.ResetRegistry()

	is_client, proto, cc_req, cc_slow, certfile, certkey, idle_timeout, trace_quicly := options.Get()

	cwd, _ := os.Getwd()
	if !filepath.IsAbs(certfile) {
		certfile = filepath.Join(cwd, certfile)
	}
	if !filepath.IsAbs(certkey) {
		certkey = filepath.Join(cwd, certkey)
	}

	cc_algo := errors.QUICLY_CC_RENO
	switch strings.ToLower(cc_req) {
	case "cubic":
		cc_algo = errors.QUICLY_CC_CUBIC
		break
	case "pico":
		cc_algo = errors.QUICLY_CC_PICO
		break
	case "reno":
		fallthrough
	default:
		cc_algo = errors.QUICLY_CC_RENO
		break
	}

	ss_algo := errors.QUICLY_SS_DISABLED
	switch strings.ToLower(cc_slow) {
	case "search":
		ss_algo = errors.QUICLY_SS_SEARCH
		break
	case "disabled":
		ss_algo = errors.QUICLY_SS_DISABLED
		break
	case "rfc2001":
		fallthrough
	default:
		ss_algo = errors.QUICLY_SS_RFC2001
		break
	}

	var is_client_int uint64 = 0
	if is_client {
		is_client_int = 1
	}
	var trace_quicly_int uint64 = 0
	if trace_quicly {
		trace_quicly_int = 1
	}
	internal.TracingOn = trace_quicly

	result := bindings.QuiclyInitializeEngine(is_client_int, proto, certfile, certkey, idle_timeout,
		uint64(cc_algo), uint64(ss_algo), trace_quicly_int)
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}

func QuiclyProcessMsg(Is_client int32, Address string, Port int32, Msg []byte, Dgram_len uint64, Conn_id uint64) int32 {
	return bindings.QuiclyProcessMsg(Is_client, Address, Port, Msg, Size_t(Dgram_len), Size_t(Conn_id))
}

func QuiclyConnect(Address string, Port int32, Conn_id uint64) int32 {
	return bindings.QuiclyConnect(Address, Port, Size_t(Conn_id))
}

func QuiclyOpenStream(Conn_id uint64, Stream_id *uint64) int32 {
	return bindings.QuiclyOpenStream(Size_t(Conn_id), (*Size_t)(Stream_id))
}

func QuiclyCloseStream(Conn_id uint64, Stream_id uint64, Error int32) int32 {
	return bindings.QuiclyCloseStream(Size_t(Conn_id), Size_t(Stream_id), Error)
}

func QuiclyClose(Conn_id uint64, Error int32) int32 {
	return bindings.QuiclyClose(Size_t(Conn_id), Error)
}

func QuiclyOutgoingMsgQueue(Id uint64, Dgram []Iovec, Num_dgrams *uint64) int32 {
	return bindings.QuiclyOutgoingMsgQueue(Size_t(Id), Dgram, (*Size_t)(Num_dgrams))
}

func QuiclyWriteStream(Conn_id uint64, Stream_id uint64, Msg []byte, Dgram_len uint64) int32 {
	return bindings.QuiclyWriteStream(Size_t(Conn_id), Size_t(Stream_id), Msg, Size_t(Dgram_len))
}

func QuiclySendDatagram(Conn_id uint64, Msg []byte, Dgram_len uint64) int32 {
	return bindings.QuiclySendDatagram(Size_t(Conn_id), Msg, Size_t(Dgram_len))
}

func safeClose[T any](dest chan T) {
	defer recover()
	close(dest)
}

func IovecToBytes(data Iovec) []byte {
	return bindings.IovecToBytes(data)
}
