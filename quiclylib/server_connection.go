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

func (s *QServerSession) init() {
	if s.started {
		return
	}
	s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

	s.streamAcceptQueue = make(chan types.Stream)
	s.handlers = make(map[uint64]*remoteClientHandler)

	s.handlersWaiter.Add(2)
	go s.connectionInHandler()
	go s.channelsWatcher()
	s.started = true
}

func (s *QServerSession) enterCritical() {
	//s.Logger.Warn().Msgf("Enter Critical section >>")
	exclusiveLock.Lock()
	//s.Logger.Warn().Msgf("Enter Critical section <<")
}
func (s *QServerSession) exitCritical() {
	//s.Logger.Warn().Msgf("Exit Critical section >>")
	exclusiveLock.Unlock()
	//s.Logger.Warn().Msgf("Exit Critical section <<")
}

func (s *QServerSession) enqueueStreamAccept(stream types.Stream) {
	s.streamAcceptQueue <- stream
}

func (s *QServerSession) handlerAdd(addr *net.UDPAddr) *remoteClientHandler {
	addrHash := addrToHash(addr)

	s.enterCritical()
	var targetHandler = s.handlers[addrHash]
	s.exitCritical()

	if targetHandler == nil {
		targetHandler = &remoteClientHandler{}
		targetHandler.init(s, addr)

		s.enterCritical()
		s.handlers[targetHandler.id] = targetHandler
		s.exitCritical()
	}
	return targetHandler
}
func (s *QServerSession) handlerDelete(id uint64) {
	bindings.RemoveConnection(id)

	s.enterCritical()
	defer s.exitCritical()

	var deleteHash uint64 = 0
	for hash, handler := range s.handlers {
		if handler.id == id {
			deleteHash = hash
			break
		}
	}
	delete(s.handlers, deleteHash)
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
		s.Logger.Debug().Msgf("[conn:%v] str:%d", s.id, len(s.handlers))
	}
}

func (s *QServerSession) connectionInHandler() {
	defer func() {
		if err := recover(); err != nil {
			s.Logger.Error().Msgf("PANIC: %v", err)
		}
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

		s.Logger.Info().Msgf("CONN READ: %d (%v)", n, addr)
		pkt := &types.Packet{
			Data:       buf[:n],
			DataLen:    n,
			RetAddress: addr,
		}

		s.Logger.Info().Msgf("CONN GET HANDLER")
		targetHandler := s.handlerAdd(addr)
		s.Logger.Info().Msgf("CONN HANDLER")
		targetHandler.receiveIncomingPacket(pkt)
		s.Logger.Info().Msgf("CONN READ SENT: %d", n)
	}
}

func (s *QServerSession) Accept() (net.Conn, error) {
	s.init()

	for {
		select {
		case st := <-s.streamAcceptQueue:
			s.Logger.Info().Msgf("QUICLY accepted new stream: %v", st)
			return st, nil
		case <-s.Ctx.Done():
			s.Logger.Error().Msgf("Server connection context closed")
			return nil, io.ErrClosedPipe
		case <-time.After(1 * time.Millisecond):
			break
		default:
		}
	}
}

func (s *QServerSession) StreamPacket(packet *types.Packet) {
	panic("not implemented")
}

func (s *QServerSession) ID() uint64 {
	return s.id
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
	s.enterCritical()
	for _, handler := range s.handlers {
		handler.Close()
	}
	s.handlers = nil
	s.exitCritical()

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
	s.enterCritical()
	defer s.exitCritical()

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
