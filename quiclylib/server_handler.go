package quiclylib

import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"net"
	"runtime/debug"
	"sync"
	"time"

	log "github.com/rs/zerolog"
)

type remoteClientHandler struct {
	// exported fields
	Ctx    context.Context
	Conn   *net.UDPConn
	Logger log.Logger

	// unexported fields
	id          uint64
	started     bool
	session     *QServerSession
	returnAddr  *net.UDPAddr
	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	cancelFunc     context.CancelFunc
	routinesWaiter sync.WaitGroup

	incomingQueue chan *types.Packet
	outgoingQueue chan *types.Packet
}

var _ types.Session = &remoteClientHandler{}

func (r *remoteClientHandler) init(session *QServerSession, addr *net.UDPAddr) uint64 {
	if r.started {
		return r.id
	}

	session.Logger.Info().Msgf("Client handler init: %v / %v", session, addr)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.id = addrToHash(addr)

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.Conn = session.Conn
	r.Logger = session.Logger
	r.returnAddr = addr

	r.streams = make(map[uint64]types.Stream)

	r.incomingQueue = make(chan *types.Packet)
	r.outgoingQueue = make(chan *types.Packet)

	r.routinesWaiter.Add(2)
	r.started = true

	go r.handlerProcess()
	go r.handlerOutgoing()

	return r.id
}

func (r *remoteClientHandler) receiveIncomingPacket(pkt *types.Packet) {
	if !r.started || pkt == nil {
		return
	}
	if pkt.RetAddress == nil {
		pkt.RetAddress = r.returnAddr
	}
	r.incomingQueue <- pkt
}

func (r *remoteClientHandler) StreamPacket(packet *types.Packet) {
	if !r.started || packet == nil {
		return
	}
	r.outgoingQueue <- packet
}

func (r *remoteClientHandler) Accept() (net.Conn, error) {
	return nil, nil
}

func (r *remoteClientHandler) Close() error {
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
	r.started = false

	r.session.handlerDelete(r.id)
	r.Logger.Info().Msgf("Closed stream %d", r.id)

	return nil
}

func (r *remoteClientHandler) Addr() net.Addr {
	return r.returnAddr
}

func (r *remoteClientHandler) ID() uint64 {
	return r.id
}

func (r *remoteClientHandler) OpenStream() types.Stream {
	return nil
}

func (r *remoteClientHandler) OnStreamOpen(streamId uint64) {
	if len(r.streams) == 0 {
		r.session.OnConnectionOpen(r)
	}

	st := &QStream{
		session: r,
		conn:    r.Conn,
		id:      streamId,
		Logger:  r.Logger,
	}
	r.streamsLock.Lock()
	r.streams[streamId] = st
	r.streamsLock.Unlock()

	r.session.enqueueStreamAccept(st)

	r.session.OnStreamOpen(streamId)

	st.OnOpened()
}

func (r *remoteClientHandler) OnStreamClose(streamId uint64, code int) {
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

func (r *remoteClientHandler) GetStream(id uint64) types.Stream {
	r.streamsLock.Lock()
	defer r.streamsLock.Unlock()
	return r.streams[id]
}

func (r *remoteClientHandler) handlerProcess() {
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
			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					r.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", r.id, pkt.DataLen, err)
				} else {
					r.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", r.id, pkt.DataLen, err)
				}
				continue
			}

			bindings.RegisterConnection(r, r.id)
			break

		case <-r.Ctx.Done():
			return

		default:
			break
		}

		r.flushOutgoingQueue()

		//_, ok := bindings.GetConnection(r.id)
		//if !ok {
		//	panic("connection closed")
		//}
	}
}

func (r *remoteClientHandler) handlerOutgoing() {
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

func (r *remoteClientHandler) flushOutgoingQueue() {
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

		_ = r.Conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := r.Conn.WriteToUDP(data, r.returnAddr)
		r.Logger.Info().Msgf("SEND packet of len %d [%v]", n, err)
	}
}
