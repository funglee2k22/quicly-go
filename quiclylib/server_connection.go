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
		return
	}

	session.Logger.Debug().Msgf("Server handler init: %v", addr)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.NetConn = session.NetConn
	r.Logger = session.Logger
	r.returnAddr = addr
	r.returnHash = addrHash
	r.lastActivity = time.Now()

	r.streams = make(map[uint64]types.Stream)
	r.streamAcceptQueue = make(chan types.Stream, 32)

	r.incomingQueue = make(chan *types.Packet, 1024)
	r.outgoingQueue = make(chan *types.Packet, 1024)

	r.routinesWaiter.Add(2)
	r.started = true

	go r.connectionProcess()
	go r.connectionOutgoing()
}

func (r *QServerConnection) refreshActivity() {
	r.lastActivity = time.Now()
}

func (r *QServerConnection) checkActivity() bool {
	if time.Now().Sub(r.lastActivity).Seconds() >= 3 {
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

func (r *QServerConnection) connectionProcess() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			//_ = r.Close()
		}
	}()

	for {
		<-time.After(1 * time.Millisecond)
		if !r.checkActivity() {
			go r.Close()
			return
		}

		select {
		case pkt := <-r.incomingQueue:
			if pkt == nil {
				r.Logger.Error().Msgf("CONN PROC ERR %v", pkt)
				break
			}

			r.refreshActivity()

			err := r.handleProcessPacket(pkt)
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

func (r *QServerConnection) handleProcessPacket(pkt *types.Packet) int32 {
	addr, port := pkt.Address()

	var ptr_id bindings.Size_t = 0

	err := bindings.QuiclyProcessMsg(int32(0), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), &ptr_id)

	r.id = uint64(ptr_id)
	bindings.RegisterConnection(r, r.id)

	return err
}

func (r *QServerConnection) connectionOutgoing() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			//_ = r.Close()
		}
	}()

	for {
		<-time.After(10 * time.Millisecond)
		if !r.checkActivity() {
			go r.Close()
			return
		}

		if r.flushOutgoingQueue() != bindings.QUICLY_OK {
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

		_ = r.NetConn.SetWriteDeadline(time.Now().Add(1 * time.Second))

		n, err := r.NetConn.WriteToUDP(data, r.returnAddr)
		r.Logger.Debug().Msgf("[%v] WRITE packet %d bytes [%v]", r.id, n, err)

		r.refreshActivity()
	}

	runtime.KeepAlive(num_packets)
	runtime.KeepAlive(packets_buf)

	return bindings.QUICLY_OK
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
	r.exitCritical(false)

	r.streamAcceptQueue <- st

	r.session.OnStreamOpen(streamId)

	st.OnOpened()
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
	shouldTerm := len(r.streams) == 0
	r.exitCritical(false)

	if r.session.OnStreamCloseCallback != nil {
		r.session.OnStreamCloseCallback(st, code)
	}

	if !shouldTerm {
		return
	}
	go func() {
		<-time.After(3 * time.Second)
		r.enterCritical(false)
		shouldTermNow := len(r.streams) == 0
		r.exitCritical(false)
		if shouldTermNow {
			r.Logger.Debug().Msgf(">> Closing parent: %d\n", r.id)
			r.Close()
			r.Logger.Debug().Msgf("<< Closing parent: %d\n", r.id)
		}
	}()
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
	r.enterCritical(false)
	if !r.started {
		r.exitCritical(false)
		return nil
	}
	r.started = false

	r.Logger.Debug().Msgf("== Connections %v WaitEnd ==\"", r.id)
	defer r.Logger.Debug().Msgf("== Connections %v End ==\"", r.id)

	r.cancelFunc()

	for _, stream := range r.streams {
		r.Logger.Warn().Msgf(">> Trying to close stream %d:%d", r.id, stream.ID())
		stream.Close()
		r.Logger.Warn().Msgf(">> Closed stream %d:%d", r.id, stream.ID())
	}
	close(r.incomingQueue)
	close(r.outgoingQueue)
	close(r.streamAcceptQueue)

	r.exitCritical(false)

	r.routinesWaiter.Wait()

	r.session.connectionDelete(r.id)

	return nil
}

func (r *QServerConnection) Addr() net.Addr {
	return r.returnAddr
}
