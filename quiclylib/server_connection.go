package quiclylib

import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"io"
	"net"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	log "github.com/rs/zerolog"
)

type QServerConnection struct {
	// exported fields
	Ctx     context.Context
	NetConn *net.UDPConn
	Logger  log.Logger

	// unexported fields
	id         uint64
	started    bool
	session    *QServerSession
	returnAddr *net.UDPAddr
	returnHash uint64

	streams           map[uint64]types.Stream
	streamsLock       sync.RWMutex
	streamAcceptQueue chan types.Stream

	cancelFunc     context.CancelFunc
	routinesWaiter sync.WaitGroup

	incomingQueue chan *types.Packet
	outgoingQueue chan *types.Packet
}

const (
	MAX_CONNECTIONS = 8192
)

var _ types.Session = &QServerConnection{}

func (r *QServerConnection) enterCritical(readonly bool) {
	//r.Logger.Warn().Msgf("Enter Critical section >>")
	if readonly {
		r.streamsLock.RLock()
	} else {
		r.streamsLock.Lock()
	}
	//r.Logger.Warn().Msgf("Enter Critical section <<")
}
func (r *QServerConnection) exitCritical(readonly bool) {
	//r.Logger.Warn().Msgf("Exit Critical section >>")
	if readonly {
		r.streamsLock.RUnlock()
	} else {
		r.streamsLock.Unlock()
	}
	//r.Logger.Warn().Msgf("Exit Critical section <<")
}

func (r *QServerConnection) init(session *QServerSession, addr *net.UDPAddr, addrHash uint64) {
	if r.started {
		return
	}

	session.Logger.Info().Msgf("Server handler init: %v", addr)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.NetConn = session.NetConn
	r.Logger = session.Logger
	r.returnAddr = addr
	r.returnHash = addrHash

	r.streams = make(map[uint64]types.Stream)
	r.streamAcceptQueue = make(chan types.Stream, 32)

	r.incomingQueue = make(chan *types.Packet, 32)
	r.outgoingQueue = make(chan *types.Packet, 32)

	r.routinesWaiter.Add(2)
	r.started = true

	go r.connectionProcess()
	go r.connectionOutgoing()
}

func (r *QServerConnection) receiveIncomingPacket(pkt *types.Packet) {
	if !r.started || pkt == nil {
		return
	}
	if pkt.RetAddress == nil {
		pkt.RetAddress = r.returnAddr
	}
	r.incomingQueue <- pkt
}

func (r *QServerConnection) connectionProcess() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			_ = r.Close()
		}
		r.Logger.Info().Msgf("CONN PROCESS END")
	}()

	for {
		<-time.After(1 * time.Millisecond)

		select {
		case pkt := <-r.incomingQueue:
			if pkt == nil {
				r.Logger.Error().Msgf("CONN PROC ERR %v", pkt)
				break
			}
			r.Logger.Info().Msgf("CONN PROC %d: %d", pkt.Streamid, pkt.DataLen)

			addr, port := pkt.Address()

			var ptr_id bindings.Size_t = 0

			err := bindings.QuiclyProcessMsg(int32(0), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), &ptr_id)

			r.id = uint64(ptr_id)
			bindings.RegisterConnection(r, r.id)

			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					r.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", r.id, pkt.DataLen, err)
				} else {
					r.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", r.id, pkt.DataLen, err)
				}
				continue
			}
			break

		case pkt := <-r.outgoingQueue:
			if pkt == nil {
				r.Logger.Error().Msgf("CONN PROC ERR %v", pkt)
				break
			}
			r.Logger.Info().Msgf("CONN PROC %d: %d", pkt.Streamid, pkt.DataLen)

			addr, port := pkt.Address()

			var ptr_id bindings.Size_t = 0

			err := bindings.QuiclyProcessMsg(int32(0), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), &ptr_id)

			r.id = uint64(ptr_id)
			bindings.RegisterConnection(r, r.id)

			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					r.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", r.id, pkt.DataLen, err)
				} else {
					r.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", r.id, pkt.DataLen, err)
				}
				continue
			}
			break

		case <-r.Ctx.Done():
			return

		default:
			break
		}

		r.flushOutgoingQueue()
	}
}

func (r *QServerConnection) connectionOutgoing() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			_ = r.Close()
		}
		r.Logger.Info().Msgf("CONN OUTGOING END")
	}()

	for {
		<-time.After(1 * time.Millisecond)

		r.enterCritical(false)
		for id, sr := range r.streams {
			stream := sr.(*QStream)
			stream.init()

			stream.outBufferLock.Lock()
			data := append([]byte{}, stream.streamOutBuf.Bytes()...)
			stream.streamOutBuf.Reset()
			stream.outBufferLock.Unlock()

			if len(data) == 0 {
				continue
			}

			errcode := bindings.QuiclyWriteStream(bindings.Size_t(r.session.ID()), bindings.Size_t(id), data, bindings.Size_t(len(data)))
			if errcode != errors.QUICLY_OK {
				r.Logger.Error().Msgf("%v quicly errorcode: %d", r.id, errcode)
				continue
			}
		}
		r.exitCritical(false)

		r.flushOutgoingQueue()

		select {
		case <-r.Ctx.Done():
			return

		default:
			break
		}
	}
}

func (r *QServerConnection) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(r.session.ID()), packets_buf, &num_packets)

	switch ret {
	case bindings.QUICLY_ERROR_NOT_OPEN:
		r.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN", ret)
		r.Close()
		return
	default:
		r.Logger.Info().Msgf("QUICLY Send failed: %v", ret)
		return
	case bindings.QUICLY_OK:
		break
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = r.NetConn.SetWriteDeadline(time.Now().Add(2 * time.Millisecond))

		n, err := r.NetConn.WriteToUDP(data, r.returnAddr)
		r.Logger.Info().Msgf("[%v] SEND packet %d bytes [%v]", r.id, n, err)
	}

	//for i := 0; i < int(num_packets); i++ {
	//	packets_buf[i].Free() // realize the struct copy from C -> go
	//}
	runtime.KeepAlive(num_packets)
	runtime.KeepAlive(packets_buf)
}

// --- Session interface --- //

func (r *QServerConnection) StreamPacket(packet *types.Packet) {
	if !r.started || packet == nil {
		return
	}
	r.Logger.Info().Msgf("ON SEND PACKET")
	r.outgoingQueue <- packet
}

func (r *QServerConnection) ID() uint64 {
	return r.id
}

func (r *QServerConnection) OpenStream() types.Stream {
	return nil
}

func (r *QServerConnection) OnStreamOpen(streamId uint64) {
	if len(r.streams) == 0 {
		r.session.enqueueConnAccept(r)
		r.session.OnConnectionOpen(r)
	}

	st := &QStream{
		session: r,
		conn:    r.NetConn,
		id:      streamId,
		Logger:  r.Logger,
	}
	r.enterCritical(false)
	r.streams[streamId] = st
	r.exitCritical(false)

	st.init()
	r.streamAcceptQueue <- st

	r.session.OnStreamOpen(streamId)

	st.OnOpened()
}

func (r *QServerConnection) OnStreamClose(streamId uint64, code int) {
	r.Logger.Info().Msgf(">> On close stream: %d\n", streamId)
	defer r.Logger.Info().Msgf("<< On close stream: %d\n", streamId)

	st := r.GetStream(streamId)
	if st == nil {
		return
	}

	_ = st.OnClosed()

	r.enterCritical(false)
	r.Logger.Info().Msgf("stream closed %d", streamId)
	delete(r.streams, streamId)
	r.exitCritical(false)

	if r.session.OnStreamCloseCallback != nil {
		r.session.OnStreamCloseCallback(st, code)
	}
}

func (r *QServerConnection) GetStream(id uint64) types.Stream {
	r.enterCritical(true)
	defer r.exitCritical(true)
	return r.streams[id]
}

// --- Listener interface --- //

func (r *QServerConnection) Accept() (types.Stream, error) {
	for {
		select {
		case st := <-r.streamAcceptQueue:
			return st, nil
		case <-r.Ctx.Done():
			return nil, io.ErrClosedPipe
		case <-time.After(1 * time.Millisecond):
			break
		}
	}
}

func (r *QServerConnection) Close() error {
	if !r.started {
		return nil
	}

	r.Logger.Info().Msgf("Closing connection %d...", r.id)
	r.cancelFunc()

	r.enterCritical(false)
	for _, stream := range r.streams {
		stream.Close()
	}
	r.exitCritical(false)

	r.routinesWaiter.Wait()
	close(r.incomingQueue)
	close(r.outgoingQueue)
	close(r.streamAcceptQueue)
	r.started = false

	r.session.connectionDelete(r.id)
	r.Logger.Info().Msgf("Closed stream %d", r.id)

	return nil
}

func (r *QServerConnection) Addr() net.Addr {
	return r.returnAddr
}
