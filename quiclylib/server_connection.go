package quiclylib

import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"io"
	"math/rand"
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
	uuid       uint64
	started    bool
	session    *QServerSession
	returnAddr *net.UDPAddr
	returnHash uint64

	lastActivity  time.Time
	exclusiveLock sync.RWMutex

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
		session.Logger.Warn().Msgf("Server handler was already initialized: %v (%v)", addr, r.uuid)
		return
	}

	r.uuid = rand.Uint64()
	session.Logger.Info().Msgf("Server handler init: %v (%v)", addr, r.uuid)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.started = true

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.NetConn = session.NetConn
	r.Logger = session.Logger
	r.returnAddr = addr
	r.returnHash = addrHash
	r.lastActivity = time.Now()

	r.streams = make(map[uint64]types.Stream)
	r.streamAcceptQueue = make(chan types.Stream, 256)

	r.incomingQueue = make(chan *types.Packet, 4192)
	r.outgoingQueue = make(chan *types.Packet, 4192)

	r.routinesWaiter.Add(3)

	go r.connectionProcess()
	go r.connectionOutgoing()
	go r.connectionSyncHandler()

	go func() {
		r.routinesWaiter.Wait()
		r.Close()
	}()
}

func (r *QServerConnection) refreshActivity() {
	r.lastActivity = time.Now()
}

func (r *QServerConnection) checkActivity() bool {
	if time.Now().Sub(r.lastActivity).Seconds() >= 30 {
		r.Logger.Error().Msgf("QServerConnection Activity fail: %v", time.Now().Sub(r.lastActivity))
		return false
	}
	return true
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

func (r *QServerConnection) handleProcessPacket(pkt *types.Packet) int32 {
	addr, port := pkt.Address()

	var ptr_id bindings.Size_t = 0

	err := bindings.QuiclyProcessMsg(int32(0), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), &ptr_id)

	r.id = uint64(ptr_id)
	bindings.RegisterConnection(r, r.id)

	return err
}

func (r *QServerConnection) flushOutgoingQueue() int32 {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(r.session.ID()), packets_buf, &num_packets)

	switch ret {
	case bindings.QUICLY_ERROR_NOT_OPEN:
		r.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN", ret)
		return ret
	default:
		r.Logger.Debug().Msgf("QUICLY Send failed: %v", ret)
		return ret
	case bindings.QUICLY_OK:
		if int(num_packets) == 0 {
			return ret
		}
		break
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = r.NetConn.SetWriteDeadline(time.Now().Add(5 * time.Millisecond))

		n, err := r.NetConn.WriteToUDP(data, r.returnAddr)
		r.Logger.Debug().Msgf("[%v] WRITE packet %d bytes [%v]", r.id, n, err)

		r.refreshActivity()
	}

	runtime.KeepAlive(num_packets)
	runtime.KeepAlive(packets_buf)

	return bindings.QUICLY_OK
}

// --- Handlers routines --- //

func (r *QServerConnection) connectionSyncHandler() {
	defer func() {
		_ = recover()
		r.routinesWaiter.Done()
		r.cancelFunc()
	}()

	r.Logger.Info().Msgf("CONN SYNC START %v", r.uuid)
	defer r.Logger.Info().Msgf("CONN SYNC END %v", r.uuid)

	for {
		select {
		case <-r.Ctx.Done():
			return
		default:
			r.streamsLock.RLock()
			for _, stream := range r.streams {
				if !stream.Sync() {
					r.refreshActivity()
				}
			}
			r.streamsLock.RUnlock()
			break
		}
	}
}

func (r *QServerConnection) connectionProcess() {
	defer func() {
		r.routinesWaiter.Done()
		r.cancelFunc()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			//_ = r.Close()
		}
	}()

	r.Logger.Info().Msgf("CONN PROC START %v", r.uuid)
	defer r.Logger.Info().Msgf("CONN PROC END %v", r.uuid)

	for {
		<-time.After(1 * time.Millisecond)
		if !r.checkActivity() {
			return
		}

		select {
		case pkt := <-r.incomingQueue:
			if pkt == nil {
				r.Logger.Error().Msgf("CONN PROC ERR %v", pkt)
				break
			}
			r.Logger.Debug().Msgf("CONN PROC %v", pkt.DataLen)

			r.refreshActivity()

			ret := r.handleProcessPacket(pkt)
			switch ret {
			case bindings.QUICLY_ERROR_NOT_OPEN:
				r.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN")
				return
			case bindings.QUICLY_ERROR_PACKET_IGNORED:
				r.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", r.id, pkt.DataLen, ret)
				continue
			default:
				r.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", r.id, pkt.DataLen, ret)
				break
			case bindings.QUICLY_OK:
				break
			}

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
		r.cancelFunc()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			//_ = r.Close()
		}
	}()

	r.Logger.Info().Msgf("CONN OUT START %v", r.uuid)
	defer r.Logger.Info().Msgf("CONN OUT END %v", r.uuid)

	for {
		<-time.After(1 * time.Millisecond)
		if !r.checkActivity() {
			return
		}

		ret := r.flushOutgoingQueue()
		switch ret {
		case bindings.QUICLY_ERROR_NOT_OPEN:
			r.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN", ret)
			return
		case bindings.QUICLY_OK:
			break
		default:
			continue
		}

		r.refreshActivity()

		select {
		case <-r.Ctx.Done():
			return

		default:
			break
		}
	}
}

// --- Session interface --- //

func (r *QServerConnection) StreamPacket(packet *types.Packet) {
	if !r.started || packet == nil {
		return
	}
	//r.Logger.Debug().Msgf("ON SEND PACKET")
	//defer r.Logger.Debug().Msgf("ON PACKET SENT")
	//r.outgoingQueue <- packet
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
	st.init()
	st.OnOpened()
	r.exitCritical(false)

	r.streamAcceptQueue <- st

	r.session.OnStreamOpen(streamId)
}

func (r *QServerConnection) OnStreamClose(streamId uint64, code int) {
	r.Logger.Debug().Msgf(">> On close stream: %d\n", streamId)
	defer r.Logger.Debug().Msgf("<< On close stream: %d\n", streamId)

	st := r.GetStream(streamId)
	if st == nil {
		return
	}

	_ = st.OnClosed()

	r.enterCritical(false)
	delete(r.streams, streamId)
	if len(r.streams) == 0 {
		r.cancelFunc()
	}
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
			r.Close()
			return nil, io.ErrClosedPipe
		case <-time.After(1 * time.Millisecond):
			break
		}
	}
}

func (r *QServerConnection) Close() error {
	r.enterCritical(false)
	if !r.started {
		r.exitCritical(false)
		return nil
	}
	r.started = false

	r.Logger.Info().Msgf("== Connections %v WaitEnd ==\"", r.id)
	defer r.Logger.Info().Msgf("== Connections %v End ==\"", r.id)

	r.cancelFunc()

	for _, stream := range r.streams {
		go func(st types.Stream) {
			r.Logger.Warn().Msgf(">> Trying to close stream %d:%d", r.id, st.ID())
			st.Sync()
			err := st.Close()
			r.Logger.Warn().Msgf(">> Closed stream %d:%d (%v)", r.id, st.ID(), err)
		}(stream)
	}
	safeClose(r.incomingQueue)
	safeClose(r.outgoingQueue)
	safeClose(r.streamAcceptQueue)

	r.exitCritical(false)

	r.routinesWaiter.Wait()

	r.session.connectionDelete(r.id)

	var ptr_id = bindings.Size_t(r.id)
	var err = bindings.QuiclyClose(ptr_id, 0)
	r.Logger.Warn().Msgf(">> Quicly Close %d(%v): %v", r.id, r.uuid, err)

	return nil
}

func (s *QServerConnection) IsClosed() bool {
	return !s.started
}

func (r *QServerConnection) Addr() net.Addr {
	return r.returnAddr
}
