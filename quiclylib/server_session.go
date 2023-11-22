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
	NetConn *net.UDPConn
	Ctx     context.Context
	Logger  log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id        uint64
	started   bool
	ctxCancel context.CancelFunc

	connectionsWaiter sync.WaitGroup
	connections       map[uint64]*QServerConnection
	connAcceptQueue   chan *QServerConnection
}

var _ types.Session = &QServerSession{}

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

	s.connections = make(map[uint64]*QServerConnection)

	s.connectionsWaiter.Add(1)
	go s.connectionInHandler()
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

func (s *QServerSession) enqueueConnAccept(conn *QServerConnection) {
	if conn != nil {
		s.connAcceptQueue <- conn
	}
}

func (s *QServerSession) connectionAdd(addr *net.UDPAddr) *QServerConnection {
	addrHash := addrToHash(addr)

	s.enterCritical()
	var targetHandler = s.connections[addrHash]
	s.exitCritical()

	if targetHandler == nil {
		targetHandler = &QServerConnection{}
		targetHandler.init(s, addr)

		s.enterCritical()
		s.connections[targetHandler.id] = targetHandler
		s.exitCritical()
	}
	return targetHandler
}
func (s *QServerSession) connDelete(id uint64) {
	bindings.RemoveConnection(id)

	s.enterCritical()
	defer s.exitCritical()

	var deleteHash uint64 = 0
	for hash, handler := range s.connections {
		if handler.id == id {
			deleteHash = hash
			break
		}
	}
	delete(s.connections, deleteHash)
}

func (s *QServerSession) connectionInHandler() {
	defer func() {
		if err := recover(); err != nil {
			s.Logger.Error().Msgf("PANIC: %v", err)
		}
		s.ctxCancel()
		runtime.UnlockOSThread()
		s.connectionsWaiter.Done()
		s.Logger.Info().Msgf("SESSION IN END")
	}()

	var buffList = make([][]byte, 0, 128)

	_ = s.NetConn.SetReadBuffer(SMALL_BUFFER_SIZE)

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

		_ = s.NetConn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))

		n, addr, err := s.NetConn.ReadFromUDP(buffList[0])
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
		conn := s.connectionAdd(addr)
		s.Logger.Info().Msgf("CONN HANDLER")
		conn.receiveIncomingPacket(pkt)
		s.Logger.Info().Msgf("CONN READ SENT: %d", n)
	}
}

func (s *QServerSession) Accept() (types.ServerConnection, error) {
	s.init()
	defer s.Logger.Info().Msgf("ServerSession terminated")

	for {
		select {
		case st := <-s.connAcceptQueue:
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
	panic(errors.QUICLY_ERROR_FAILED)
}

func (s *QServerSession) ID() uint64 {
	return s.id
}

func (s *QServerSession) Close() error {
	if s.NetConn == nil {
		return nil
	}
	defer func() {
		s.NetConn = nil
		if s.OnConnectionClose != nil {
			s.OnConnectionClose(s)
		}
	}()
	s.enterCritical()
	for _, handler := range s.connections {
		handler.Close()
	}
	s.connections = nil
	s.exitCritical()

	s.connectionsWaiter.Wait()
	return s.NetConn.Close()
}

func (s *QServerSession) Addr() net.Addr {
	if s.NetConn == nil {
		return nil
	}
	return s.NetConn.LocalAddr()
}

func (s *QServerSession) OpenStream() types.Stream {
	return nil
}

func (s *QServerSession) GetStream(id uint64) types.Stream {
	st, _ := s.getStreamInternal(id)
	return st
}

func (s *QServerSession) getStreamInternal(id uint64) (types.Stream, *QServerConnection) {
	s.enterCritical()
	defer s.exitCritical()

	for _, handler := range s.connections {
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
