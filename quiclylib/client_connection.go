package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"time"
)

type QClientSession struct {
	// exported fields
	Conn   *net.UDPConn
	Ctx    context.Context
	Logger log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id             uint64
	connected      bool
	closing        bool
	ctxCancel      context.CancelFunc
	handlersWaiter sync.WaitGroup

	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	exclusiveLock sync.RWMutex

	firstStreamOpen    bool
	waitStreamOpenLock sync.RWMutex

	incomingQueue chan *types.Packet
}

var _ net.Listener = &QClientSession{}
var _ types.Session = &QClientSession{}

func (s *QClientSession) enterCritical(readonly bool) {
	// s.Logger.Warn().Msgf("Will Enter Critical section (%v)", readonly)
	if readonly {
		s.streamsLock.RLock()
	} else {
		s.streamsLock.Lock()
	}
	// s.Logger.Warn().Msgf("Enter Critical section (%v)", readonly)
}
func (s *QClientSession) exitCritical(readonly bool) {
	// s.Logger.Warn().Msgf("Will Exit Critical section (%v)", readonly)
	if readonly {
		s.streamsLock.RUnlock()
	} else {
		s.streamsLock.Unlock()
	}
	// s.Logger.Warn().Msgf("Exit Critical section (%v)", readonly)
}

func (s *QClientSession) init() {
	if s.incomingQueue == nil {
		s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

		s.id = rand.Uint64()
		s.Logger.Info().Msgf("Client handler init: %v", s.id)

		s.incomingQueue = make(chan *types.Packet, 1024)
		s.streams = make(map[uint64]types.Stream)
	} else {
		s.Logger.Warn().Msgf("Client handler was already init: %v", s.id)
	}
}

func (s *QClientSession) connect() int {
	if s.connected {
		return errors.QUICLY_OK
	}

	s.init()

	ptrId := bindings.Size_t(s.id)

	udpAddr := s.Addr().(*net.UDPAddr)

	if ret := bindings.QuiclyConnect(udpAddr.IP.String(), int32(udpAddr.Port), ptrId); ret != errors.QUICLY_OK {
		return int(ret)
	}

	bindings.RegisterConnection(s, s.id)

	s.connected = true
	s.closing = false

	s.handlersWaiter.Add(2)
	go s.connectionInHandler()
	go s.connectionProcessHandler()

	go func() {
		s.handlersWaiter.Wait()
		s.Close()
	}()

	if s.OnConnectionOpen != nil {
		s.OnConnectionOpen(s)
	}

	return errors.QUICLY_OK
}

// --- Handlers routines --- //

func (s *QClientSession) connectionInHandler() {
	defer func() {
		_ = recover()
		s.ctxCancel()
		s.handlersWaiter.Done()
	}()

	var buffList = make([][]byte, 0, 128)
	for i := 0; i < 128; i++ {
		buffList = append(buffList, make([]byte, SMALL_BUFFER_SIZE))
	}

	s.Logger.Info().Msgf("CONN IN START %v", s.id)
	defer s.Logger.Info().Msgf("CONN IN END %v", s.id)

	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
			break
		}

		s.Conn.SetReadDeadline(time.Now().Add(1 * time.Second))

		n, addr, err := s.Conn.ReadFromUDP(buffList[0])
		s.Logger.Debug().Msgf("[%v] UDP packet %d %v", s.id, n, addr)
		if n == 0 || (n == 0 && err != nil) {
			s.Logger.Debug().Msgf("QUICLY No packet")
			continue
		}

		buf := buffList[0]
		buffList = buffList[1:]
		if len(buffList) == 0 {
			for i := 0; i < 128; i++ {
				buffList = append(buffList, make([]byte, SMALL_BUFFER_SIZE))
			}
		}

		pkt := &types.Packet{
			Data:       buf[:n],
			DataLen:    n,
			RetAddress: addr,
		}
		s.incomingQueue <- pkt
	}
}

func (s *QClientSession) connectionProcessHandler() {
	returnAddr := s.Conn.RemoteAddr().(*net.UDPAddr)
	defer func() {
		_ = recover()
		s.ctxCancel()
		s.handlersWaiter.Done()
	}()

	s.Logger.Info().Msgf("CONN PROC START %v", s.id)
	defer s.Logger.Info().Msgf("CONN PROC END %v", s.id)

	buffer := make([]*types.Packet, 0, 32)

	for {
		select {
		case <-s.Ctx.Done():
			return

		case pkt := <-s.incomingQueue:
			buffer = append(buffer, pkt)
			break

		case <-time.After(1 * time.Millisecond):
			for _, pkt := range buffer {
				if len(s.streams) == 0 {
					s.Logger.Debug().Msgf("[%v] No active streams", s.id)
					break
				}

				s.Logger.Debug().Msgf("[%v] PROC packet %v %d(%v)", s.id, s.id, pkt.DataLen, pkt.Streamid)
				if pkt == nil {
					break
				}
				addr, port := pkt.Address()
				if len(addr) == 0 || port == -1 {
					addr, port = returnAddr.IP.String(), returnAddr.Port
				}

				err := bindings.QuiclyProcessMsg(int32(1), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), bindings.Size_t(s.id))
				if err != bindings.QUICLY_OK {
					if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
						s.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", s.id, pkt.DataLen, err)
					} else {
						s.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", s.id, pkt.DataLen, err)
					}
				}
			}
			buffer = buffer[:0]

			if ret := s.flushOutgoingQueue(); ret != errors.QUICLY_OK {
				return
			}
			break
		}
	}
}

func (s *QClientSession) flushOutgoingQueue() int32 {
	num_packets := bindings.Size_t(4096)
	packets_buf := make([]bindings.Iovec, 4096)

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)
	if int(num_packets) == 0 {
		return ret
	}

	switch ret {
	case bindings.QUICLY_ERROR_NOT_OPEN:
		s.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN", ret)
		return ret
	default:
		s.Logger.Debug().Msgf("QUICLY Send failed: %d - %v", num_packets, ret)
		return ret
	case bindings.QUICLY_OK:
		break
	}

	s.Logger.Debug().Msgf("CONN flush (%d) %v", num_packets, s.id)
	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = s.Conn.SetWriteDeadline(time.Now().Add(5 * time.Millisecond))

		n, err := s.Conn.Write(data)
		s.Logger.Debug().Msgf("[%v] SEND packet %d bytes [%v]", s.id, n, err)
	}

	runtime.KeepAlive(num_packets)
	runtime.KeepAlive(packets_buf)

	return ret
}

// --- Session interface --- //

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) OpenStream() types.Stream {
	s.enterCritical(false)
	if err := s.connect(); err != errors.QUICLY_OK {
		s.Logger.Error().Msgf("connect error: %d", err)
		s.exitCritical(false)
		return nil
	}
	s.exitCritical(false)

	var streamId bindings.Size_t = 0
	connId := bindings.Size_t(s.id)

	if ret := bindings.QuiclyOpenStream(connId, &streamId); ret != errors.QUICLY_OK {
		s.Logger.Debug().Msgf("open stream err")
		return nil
	}

	st := &QStream{
		session: s,
		conn:    s.Conn,
		id:      uint64(streamId),
		Logger:  s.Logger,
	}
	st.init()

	s.enterCritical(false)
	s.streams[st.id] = st
	s.exitCritical(false)

	return st
}

func (s *QClientSession) GetStream(id uint64) types.Stream {
	s.enterCritical(true)
	defer s.exitCritical(true)
	return s.streams[id]
}

func (s *QClientSession) StreamPacket(packet *types.Packet) {
	//defer func() {
	//	_ = recover()
	//}()
	//if packet == nil || s.outgoingQueue == nil {
	//	return
	//}
	//select {
	//case s.outgoingQueue <- packet:
	//	break
	//case <-time.After(3 * time.Millisecond):
	//	return
	//}
}

func (s *QClientSession) OnStreamOpen(streamId uint64) {
	s.enterCritical(true)
	st, ok := s.streams[streamId]
	s.exitCritical(true)
	if ok {
		if s.OnStreamOpenCallback != nil {
			s.OnStreamOpenCallback(st)
		}
		st.OnOpened()
	}
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	s.Logger.Info().Msgf(">> On close stream: %d\n", streamId)
	defer s.Logger.Info().Msgf("<< On close stream: %d\n", streamId)

	s.enterCritical(false)
	st, ok := s.streams[streamId]
	delete(s.streams, streamId)
	shouldTerm := len(s.streams) == 0
	s.exitCritical(false)
	if ok {
		_ = st.OnClosed()
	}

	if ok && s.OnStreamCloseCallback != nil {
		s.OnStreamCloseCallback(st, error)
	}

	if shouldTerm {
		s.Logger.Info().Msgf(">> Closing parent: %d\n", s.id)
		s.ctxCancel()
		return
	}
}

// --- Listener interface --- //

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	defer func() {
		_ = recover()
	}()
	if !s.connected || s.closing || s == nil || s.Conn == nil {
		return nil
	}
	s.Logger.Info().Msgf("== Connections %v WaitEnd ==\"", s.id)
	defer s.Logger.Info().Msgf("== Connections %v End ==\"", s.id)

	s.enterCritical(false)

	s.closing = true

	s.ctxCancel()
	// copy of stream list is to workaround lock issues
	tmpStreams := make([]types.Stream, len(s.streams))
	pos := 0
	for _, stream := range s.streams {
		tmpStreams[pos] = stream
		pos++
	}
	s.exitCritical(false)

	s.Logger.Warn().Msgf(">> streams to close: %v", tmpStreams)

	wg := &sync.WaitGroup{}
	wg.Add(len(tmpStreams))

	for _, stream := range tmpStreams {
		go func(st types.Stream) {
			defer wg.Done()

			s.Logger.Warn().Msgf(">> Trying to close stream %d / %p", st.ID(), st)
			st.Sync()
			st.Close()
			s.Logger.Warn().Msgf(">> Closed stream %d / %p", st.ID(), st)
		}(stream)
	}

	go func() {
		defer func() {
			s.closing = false
			s.connected = false
		}()
		wg.Wait()

		s.enterCritical(false)
		s.Logger.Warn().Msgf(">> Close queues %d(%v)", s.id, s.id)
		safeClose(s.incomingQueue)
		_ = s.Conn.Close()
		s.exitCritical(false)

		if s.OnConnectionClose != nil {
			s.Logger.Debug().Msgf("Close connection: %d\n", s.id)

			s.OnConnectionClose(s)
		}
		s.Logger.Warn().Msgf(">> Wait routines %d(%v)", s.id, s.id)
		s.handlersWaiter.Wait()

		bindings.RemoveConnection(s.id)

		var connId = bindings.Size_t(s.id)
		var err = bindings.QuiclyClose(connId, 0)
		s.Logger.Warn().Msgf(">> Quicly Close %d(%v): %v", s.id, s.id, err)
	}()

	return nil
}

func (s *QClientSession) IsClosed() bool {
	return !s.connected
}

func (s *QClientSession) Addr() net.Addr {
	return s.Conn.RemoteAddr()
}
