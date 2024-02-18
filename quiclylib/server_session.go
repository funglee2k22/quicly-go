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
	_, _ = h64.Write([]byte(addr.IP.String()))
	return h64.Sum64()
}

func (s *QServerSession) init() {
	if s.started {
		return
	}
	s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

	s.connections = make(map[uint64]*QServerConnection)
	s.connAcceptQueue = make(chan *QServerConnection, 32)

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
	s.Logger.Debug().Msgf("HASH: %v -> %v", addr, addrHash)

	s.enterCritical()
	var targetHandler = s.connections[addrHash]
	s.exitCritical()

	if targetHandler == nil {
		targetHandler = &QServerConnection{}
		targetHandler.init(s, addr, addrHash)

		s.enterCritical()
		s.connections[targetHandler.returnHash] = targetHandler
		s.exitCritical()
	}
	return targetHandler
}

func (s *QServerSession) connectionDelete(id uint64) {
	s.enterCritical()
	defer s.exitCritical()

	var deleteHash uint64 = 0
	for hash, handler := range s.connections {
		if handler.id == id {
			deleteHash = hash
			defer func() {
				delete(s.connections, deleteHash)
				s.Logger.Debug().Msgf("CONN DELETE %d", id)
				bindings.RemoveConnection(id)
				s.Logger.Debug().Msgf("CONN DELETED %d", id)
			}()
			return
		}
	}
}

func (s *QServerSession) connectionInHandler() {
	defer func() {
		if err := recover(); err != nil {
			s.Logger.Error().Msgf("PANIC: %v", err)
		}
		s.ctxCancel()
		runtime.UnlockOSThread()
		s.connectionsWaiter.Done()
		s.Logger.Debug().Msgf("SESSION IN END")
	}()

	var buffList = make([][]byte, 0, 128)

	_ = s.NetConn.SetReadBuffer(SMALL_BUFFER_SIZE)

	runtime.LockOSThread()

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

		_ = s.NetConn.SetReadDeadline(time.Now().Add(1 * time.Second))

		s.Logger.Debug().Msgf(">> CONN %v READ", s.id)
		n, addr, err := s.NetConn.ReadFromUDP(buffList[0])
		s.Logger.Debug().Msgf("<< CONN %v READ: %d (%v)", s.id, n, err)
		if n == 0 || (n == 0 && err != nil) {
			continue
		}

		buf := buffList[0]
		buffList = buffList[1:]

		pkt := &types.Packet{
			Data:       buf[:n],
			DataLen:    n,
			RetAddress: addr,
		}

		s.Logger.Debug().Msgf(">> CONN %v ADD: %d", s.id, n)
		conn := s.connectionAdd(addr)
		s.Logger.Debug().Msgf(">> CONN %v INCOMING: %d (handler:%p)", s.id, n, conn)
		conn.receiveIncomingPacket(pkt)
		s.Logger.Debug().Msgf("<< CONN %v SENT: %d (handler:%p)", s.id, n, conn)
	}
}

// --- Session interface --- //

func (s *QServerSession) StreamPacket(packet *types.Packet) {
	panic(errors.QUICLY_ERROR_FAILED)
}

func (s *QServerSession) ID() uint64 {
	return s.id
}

func (s *QServerSession) OpenStream() types.Stream {
	return nil
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

func (s *QServerSession) GetStream(id uint64) types.Stream {
	st, _ := s.getStreamInternal(id)
	return st
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

// --- Listener interface --- //

func (s *QServerSession) Accept() (types.ServerConnection, error) {
	s.init()

	for {
		select {
		case st := <-s.connAcceptQueue:
			s.Logger.Debug().Msgf("QUICLY accepted new stream: %v", st)
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

func (s *QServerSession) Close() error {
	if s.NetConn == nil {
		return nil
	}
	defer func() {
		s.NetConn = nil
		if s.OnConnectionClose != nil {
			s.OnConnectionClose(s)
		}
		s.Logger.Debug().Msgf("ServerSession terminated")
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
