package quiclylib

import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"io"
	"net"
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

func (r *QServerConnection) init(session *QServerSession, addr *net.UDPAddr) {
	if r.started {
		return
	}

	session.Logger.Info().Msgf("Client handler init: %v / %v", session, addr)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.NetConn = session.NetConn
	r.Logger = session.Logger
	r.returnAddr = addr

	r.streams = make(map[uint64]types.Stream)
	r.streamAcceptQueue = make(chan types.Stream)

	r.incomingQueue = make(chan *types.Packet)
	r.outgoingQueue = make(chan *types.Packet)

	r.routinesWaiter.Add(2)
	r.started = true

	go r.connProcess()
	go r.connOutgoing()
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

func (r *QServerConnection) StreamPacket(packet *types.Packet) {
	if !r.started || packet == nil {
		return
	}
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
	r.streamsLock.Lock()
	r.streams[streamId] = st
	r.streamsLock.Unlock()

	r.streamAcceptQueue <- st

	r.session.OnStreamOpen(streamId)

	st.OnOpened()
}

func (r *QServerConnection) OnStreamClose(streamId uint64, code int) {
	st := r.GetStream(streamId)
	if st == nil {
		return
	}

	_ = st.OnClosed()

	r.streamsLock.Lock()
	delete(r.streams, streamId)
	r.streamsLock.Unlock()

	if r.session.OnStreamCloseCallback != nil {
		r.session.OnStreamCloseCallback(st, code)
	}
}

func (r *QServerConnection) GetStream(id uint64) types.Stream {
	r.streamsLock.Lock()
	defer r.streamsLock.Unlock()
	return r.streams[id]
}

func (r *QServerConnection) connProcess() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			_ = r.Close()
		}
	}()

	sleepCounter := 0

	for {
		if sleepCounter > 100 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		select {
		case pkt := <-r.incomingQueue:
			if pkt == nil {
				r.Logger.Error().Msgf("HANDLER PROC ERR %v", pkt)
				break
			}
			r.Logger.Info().Msgf("HANDLER PROC %d: %d", pkt.Streamid, pkt.DataLen)

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

func (r *QServerConnection) connOutgoing() {
	defer func() {
		r.routinesWaiter.Done()
		if err := recover(); err != nil {
			r.Logger.Error().Msgf("PANIC: %v", err)
			debug.PrintStack()
			_ = r.Close()
		}
	}()

	sleepCounter := 0

	for {
		if sleepCounter > 100 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		select {
		case pkt := <-r.outgoingQueue:
			if pkt == nil {
				continue
			}

			r.streamsLock.RLock()
			var stream, _ = r.streams[pkt.Streamid].(*QStream)
			r.streamsLock.RUnlock()
			if stream == nil {
				r.Logger.Error().Msgf("HANDLER WRITE ERR: no stream %d", pkt.Streamid)
				return
			}

			r.Logger.Info().Msgf("IN 2")
			stream.outBufferLock.Lock()
			data := append([]byte{}, stream.streamOutBuf.Bytes()...)
			r.Logger.Info().Msgf("HANDLER WRITE %d: %d", pkt.Streamid, pkt.DataLen)

			stream.streamOutBuf.Reset()
			stream.outBufferLock.Unlock()
			r.Logger.Info().Msgf("OUT 2")

			errcode := bindings.QuiclyWriteStream(bindings.Size_t(r.session.ID()), bindings.Size_t(pkt.Streamid), data, bindings.Size_t(len(data)))
			if errcode != errors.QUICLY_OK {
				r.Logger.Error().Msgf("%v quicly errorcode: %d", r.id, errcode)
				continue
			}
			continue

		case <-r.Ctx.Done():
			return

		default:
			break
		}

		r.flushOutgoingQueue()
	}
}

func (r *QServerConnection) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(r.session.ID()), packets_buf, &num_packets)

	switch ret {
	case bindings.QUICLY_ERROR_FREE_CONNECTION:
		r.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_FREE_CONNECTION", ret)
		// bindings.RemoveConnection(r.id)
		return
	default:
		r.Logger.Debug().Msgf("QUICLY Send failed: %v", ret)
		return
	case bindings.QUICLY_OK:
		break
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = r.NetConn.SetWriteDeadline(time.Now().Add(2 * time.Millisecond))

		n, err := r.NetConn.WriteToUDP(data, r.returnAddr)
		r.Logger.Info().Msgf("SEND packet of len %d [%v]", n, err)
	}
}

// --- Listener interface --- //

func (r *QServerConnection) Accept() (types.Stream, error) {
	defer r.Logger.Info().Msgf("ServerConnection terminated")

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

	r.Logger.Info().Msgf("Closing stream %d...", r.id)
	r.cancelFunc()

	r.streamsLock.Lock()
	for _, stream := range r.streams {
		stream.Close()
	}
	r.streamsLock.Unlock()

	r.routinesWaiter.Wait()
	close(r.incomingQueue)
	close(r.outgoingQueue)
	close(r.streamAcceptQueue)
	r.started = false

	r.session.connDelete(r.id)
	r.Logger.Info().Msgf("Closed stream %d", r.id)

	return nil
}

func (r *QServerConnection) Addr() net.Addr {
	return r.returnAddr
}
