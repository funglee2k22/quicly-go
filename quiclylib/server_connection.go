package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"hash/fnv"
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	log "github.com/rs/zerolog"
)

var exclusiveLock sync.Mutex

type QServerSession struct {
	// exported fields
	Conn   *net.UDPConn
	Ctx    context.Context
	Logger log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id                uint64
	started           bool
	ctxCancel         context.CancelFunc
	streamAcceptQueue chan types.Stream
	handlersWaiter    sync.WaitGroup
	handlers          map[uint64]*remoteClientHandler
}

var _ types.Session = &QServerSession{}
var _ net.Listener = &QServerSession{}

func addrToHash(addr *net.UDPAddr) uint64 {
	h64 := fnv.New64a()
	_, _ = h64.Write([]byte(addr.String()))
	return h64.Sum64()
}

func (s *QServerSession) ID() uint64 {
	return s.id
}

func (s *QServerSession) start() {
	if s.started {
		return
	}
	s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

	s.streamAcceptQueue = make(chan types.Stream)

	s.handlersWaiter.Add(3)
	go s.connectionInHandler()
	go s.channelsWatcher()
	s.started = true
}

func (s *QServerSession) enqueueStreamAccept(stream types.Stream) {
	s.streamAcceptQueue <- stream
}

func (s *QServerSession) doRegister(id uint64) {
	bindings.RegisterConnection(s, id)
}

func (s *QServerSession) channelsWatcher() {
	defer s.handlersWaiter.Done()

	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
			break
		}
		//s.Logger.Info().Msgf("[conn:%v] in:%d out:%d str:%d", s.id, len(s.incomingQueue), len(s.outgoingQueue), len(s.streams))
	}
}

func (s *QServerSession) connectionInHandler() {
	defer func() {
		_ = recover()
		s.ctxCancel()
		runtime.UnlockOSThread()
		s.handlersWaiter.Done()
	}()

	var buffList = make([][]byte, 0, 128)

	_ = s.Conn.SetReadBuffer(SMALL_BUFFER_SIZE)

	runtime.LockOSThread()

	sleepCounter := 0

	for {
		if len(buffList) == 0 {
			for i := 0; i < 128; i++ {
				buffList = append(buffList, make([]byte, SMALL_BUFFER_SIZE))
			}
		}

		select {
		case <-s.Ctx.Done():
			return
		default:
			break
		}

		if sleepCounter > 1000 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		_ = s.Conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))

		n, addr, err := s.Conn.ReadFromUDP(buffList[0])
		if n == 0 || (n == 0 && err != nil) {
			s.Logger.Debug().Msgf("QUICLY No packet")
			continue
		}

		buf := buffList[0]
		buffList = buffList[1:]

		s.Logger.Info().Msgf("CONN READ: %d", n)
		pkt := &packet{
			data:    buf[:n],
			dataLen: n,
			addr:    addr,
		}

		addrHash := addrToHash(addr)

		exclusiveLock.Lock()
		var targetHandler = s.handlers[addrHash]
		if targetHandler == nil {
			targetHandler = &remoteClientHandler{}
			s.Logger.Info().Msgf("HANDLER INIT")
			targetHandler.init(s, addr)
			s.handlers[targetHandler.id] = targetHandler
		}
		exclusiveLock.Unlock()

		targetHandler.sendPacket(pkt)
	}
}

func (s *QServerSession) Accept() (net.Conn, error) {
	s.start()

	for {
		select {
		case st := <-s.streamAcceptQueue:
			s.Logger.Info().Msgf("QUICLY accepted new stream: %%v", st)
			return st, nil
		case <-s.Ctx.Done():
			return nil, io.ErrClosedPipe
		case <-time.After(1 * time.Millisecond):
			break
		default:
		}
	}
}

func (s *QServerSession) Close() error {
	if s.Conn == nil {
		return nil
	}
	defer func() {
		s.Conn = nil
		if s.OnConnectionClose != nil {
			s.OnConnectionClose(s)
		}
	}()
	exclusiveLock.Lock()
	for _, handler := range s.handlers {
		handler.Close()
	}
	s.handlers = nil
	exclusiveLock.Unlock()

	s.handlersWaiter.Wait()
	return s.Conn.Close()
}

func (s *QServerSession) Addr() net.Addr {
	if s.Conn == nil {
		return nil
	}
	return s.Conn.LocalAddr()
}

func (s *QServerSession) OpenStream() types.Stream {
	return nil
}

func (s *QServerSession) GetStream(id uint64) types.Stream {
	st, _ := s.getStreamInternal(id)
	return st
}

func (s *QServerSession) getStreamInternal(id uint64) (types.Stream, *remoteClientHandler) {
	exclusiveLock.Lock()
	defer exclusiveLock.Unlock()

	for _, handler := range s.handlers {
		if st := handler.GetStream(id); st != nil {
			return st, handler
		}
	}

	return nil, nil
}

func (s *QServerSession) OnStreamOpen(streamId uint64) {
	st, handler := s.getStreamInternal(streamId)
	if st == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	s.enqueueStreamAccept(st)

	if s.OnConnectionOpen != nil && len(handler.streams) == 1 {
		s.OnConnectionOpen(handler)
	}

	if s.OnStreamOpenCallback != nil {
		s.OnStreamOpenCallback(st)
	}
}

func (s *QServerSession) OnStreamClose(streamId uint64, error int) {
	st, _ := s.getStreamInternal(streamId)
	if st == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	if s.OnStreamCloseCallback != nil {
		s.OnStreamCloseCallback(st, error)
	}
}

// --- remote client handler --- //

type remoteClientHandler struct {
	// exported fields
	Ctx    context.Context
	Conn   *net.UDPConn
	Logger log.Logger

	// unexported fields
	id          uint64
	session     *QServerSession
	returnAddr  *net.UDPAddr
	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	cancelFunc     context.CancelFunc
	routinesWaiter sync.WaitGroup

	incomingQueue chan *packet
	outgoingQueue chan *packet
}

func (r *remoteClientHandler) init(session *QServerSession, addr *net.UDPAddr) uint64 {
	session.Logger.Info().Msgf("Client handler start: %v / %v", session, addr)

	if session == nil || addr == nil {
		panic(errors.QUICLY_ERROR_FAILED)
	}

	r.id = addrToHash(addr)

	r.session = session
	r.Ctx, r.cancelFunc = context.WithCancel(session.Ctx)
	r.Conn = session.Conn
	r.Logger = session.Logger
	r.returnAddr = addr

	r.streams = make(map[uint64]types.Stream)

	r.incomingQueue = make(chan *packet)
	r.outgoingQueue = make(chan *packet)
	return r.id
}

func (r *remoteClientHandler) sendPacket(pkt *packet) {
	r.incomingQueue <- pkt
}

func (r *remoteClientHandler) Accept() (net.Conn, error) {
	return nil, nil
}

func (r *remoteClientHandler) Close() error {
	r.cancelFunc()

	r.streamsLock.Lock()
	for _, stream := range r.streams {
		stream.Close()
	}
	r.streamsLock.Unlock()

	r.routinesWaiter.Wait()

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
	if len(r.streams) == 1 {
		r.session.OnConnectionOpen(r)
	}

	st := &QStream{
		session: r.session,
		conn:    r.Conn,
		id:      streamId,
	}
	r.streamsLock.Lock()
	r.streams[streamId] = st
	r.streamsLock.Unlock()

	r.session.enqueueStreamAccept(st)

	r.session.OnStreamOpen(streamId)
}

func (r *remoteClientHandler) OnStreamClose(streamId uint64, code int) {
	r.streamsLock.Lock()
	closedStream := r.streams[streamId]
	delete(r.streams, streamId)
	r.streamsLock.Unlock()

	if closedStream != nil && r.session.OnStreamCloseCallback != nil {
		r.session.OnStreamCloseCallback(closedStream, code)
	}
}

func (r *remoteClientHandler) GetStream(id uint64) types.Stream {
	r.streamsLock.Lock()
	defer r.streamsLock.Unlock()
	return r.streams[id]
}

var _ types.Session = &remoteClientHandler{}

func (r *remoteClientHandler) handlerProcess() {
	defer func() {
		_ = recover()
		r.routinesWaiter.Done()
	}()

	sleepCounter := 0

	for {
		select {
		case pkt := <-r.incomingQueue:
			r.Logger.Info().Msgf("CONN PROC %v", pkt)
			if pkt == nil {
				r.Logger.Error().Msgf("CONN PROC ERR %v", pkt)
				break
			}
			r.Logger.Info().Msgf("CONN PROC %d: %d", pkt.streamid, pkt.dataLen)

			addr, port := pkt.Address()

			var ptr_id bindings.Size_t = 0

			err := bindings.QuiclyProcessMsg(int32(0), addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id)
			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					r.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", r.id, pkt.dataLen, err)
				} else {
					r.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", r.id, pkt.dataLen, err)
				}
				continue
			}

			r.session.doRegister(uint64(ptr_id))
			break

		case <-r.Ctx.Done():
			return
		}

		if sleepCounter > 1000 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		r.flushOutgoingQueue()
	}
}

func (r *remoteClientHandler) handlerOutgoing() {
	defer func() {
		_ = recover()
		r.routinesWaiter.Done()
	}()

	sleepCounter := 0

	for {
		select {
		case pkt := <-r.outgoingQueue:
			if pkt == nil {
				continue
			}
			r.Logger.Info().Msgf("CONN WRITE %d: %d - %v", pkt.streamid, pkt.dataLen, pkt.data[:pkt.dataLen])

			r.streamsLock.RLock()
			var stream, _ = r.streams[pkt.streamid].(*QStream)
			r.streamsLock.RUnlock()

			stream.outBufferLock.Lock()
			data := append([]byte{}, stream.streamOutBuf.Bytes()...)
			r.Logger.Info().Msgf("STREAM WRITE %d: %d", pkt.streamid, len(data))

			stream.streamOutBuf.Reset()
			stream.outBufferLock.Unlock()

			errcode := bindings.QuiclyWriteStream(bindings.Size_t(r.id), bindings.Size_t(pkt.streamid), data, bindings.Size_t(len(data)))
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

		if sleepCounter > 1000 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		r.flushOutgoingQueue()
	}
}

func (r *remoteClientHandler) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	exclusiveLock.Lock()
	defer exclusiveLock.Unlock()

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(r.session.ID()), packets_buf, &num_packets)

	if ret != bindings.QUICLY_OK {
		r.Logger.Debug().Msgf("QUICLY Send failed: %d - %v", num_packets, ret)
		return
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = r.Conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := r.Conn.Write(data)
		r.Logger.Debug().Msgf("SEND packet of len %d [%v]", n, err)
	}
}
