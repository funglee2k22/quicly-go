package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
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
	ctxCancel      context.CancelFunc
	handlersWaiter sync.WaitGroup

	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	lastActivity  time.Time
	exclusiveLock sync.RWMutex

	firstStreamOpen    bool
	waitStreamOpenLock sync.RWMutex

	outgoingQueue chan *types.Packet
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

		s.incomingQueue = make(chan *types.Packet, 1024)
		s.outgoingQueue = make(chan *types.Packet, 1024)
		s.streams = make(map[uint64]types.Stream)
		s.lastActivity = time.Now()
	}
}

func (s *QClientSession) connect() int {
	if s.connected {
		return errors.QUICLY_OK
	}

	s.init()

	var ptr_id bindings.Size_t = 0

	udpAddr := s.Addr().(*net.UDPAddr)

	if ret := bindings.QuiclyConnect(udpAddr.IP.String(), int32(udpAddr.Port), &ptr_id); ret != errors.QUICLY_OK {
		return int(ret)
	}

	s.id = uint64(ptr_id)
	bindings.RegisterConnection(s, s.id)

	s.connected = true

	s.handlersWaiter.Add(3)
	go s.connectionInHandler()
	go s.connectionWriteHandler()
	go s.connectionProcessHandler()

	if s.OnConnectionOpen != nil {
		s.OnConnectionOpen(s)
	}

	return errors.QUICLY_OK
}

func (s *QClientSession) refreshActivity() {
	s.lastActivity = time.Now()
}

func (s *QClientSession) checkActivity() bool {
	if time.Now().Sub(s.lastActivity).Seconds() >= 3 {
		return false
	}
	return true
}

func (s *QClientSession) connectionInHandler() {
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	var buffList = make([][]byte, 0, 128)
	for i := 0; i < 128; i++ {
		buffList = append(buffList, make([]byte, SMALL_BUFFER_SIZE))
	}

	for {
		if !s.checkActivity() {
			go s.Close()
			return
		}

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

		s.refreshActivity()

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
		go func() {
			s.incomingQueue <- pkt
		}()
	}
}

func (s *QClientSession) connectionProcessHandler() {
	returnAddr := s.Conn.RemoteAddr().(*net.UDPAddr)
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	for {
		if !s.checkActivity() {
			go s.Close()
			return
		}

		select {
		case <-s.Ctx.Done():
			return

		case pkt := <-s.incomingQueue:
			if len(s.streams) == 0 {
				s.Logger.Debug().Msgf("[%v] No active streams", s.id)
				break
			}

			s.refreshActivity()

			s.Logger.Debug().Msgf("[%v] PROC packet %v %d", s.id, pkt.DataLen, pkt.Streamid)
			if pkt == nil {
				break
			}
			addr, port := pkt.Address()
			if len(addr) == 0 || port == -1 {
				addr, port = returnAddr.IP.String(), returnAddr.Port
			}

			var ptr_id bindings.Size_t = 0

			err := bindings.QuiclyProcessMsg(int32(1), addr, int32(port), pkt.Data, bindings.Size_t(pkt.DataLen), &ptr_id)
			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					s.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", s.id, pkt.DataLen, err)
				} else {
					s.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", s.id, pkt.DataLen, err)
				}
			}

			s.id = uint64(ptr_id)
			bindings.RegisterConnection(s, s.id)
			break

		case <-time.After(1 * time.Millisecond):
			break
		}

		s.flushOutgoingQueue()
	}
}

func (s *QClientSession) connectionWriteHandler() {
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	for {
		if !s.checkActivity() {
			go s.Close()
			return
		}

		select {
		case <-s.Ctx.Done():
			return

		case <-time.After(1 * time.Millisecond):
			tmpStreams := make(map[uint64]types.Stream)
			s.enterCritical(true)
			for id, stream := range s.streams {
				tmpStreams[id] = stream
			}
			s.exitCritical(true)

			for id, sr := range tmpStreams {
				stream := sr.(*QStream)
				stream.init()

				if stream.streamOutBuf.Len() == 0 {
					continue
				}

				s.refreshActivity()

				stream.outBufferLock.Lock()
				data := append([]byte{}, stream.streamOutBuf.Bytes()...)
				s.Logger.Debug().Msgf("STREAM WRITE %d: %d", id, len(data))
				stream.streamOutBuf.Reset()
				stream.outBufferLock.Unlock()

				errcode := bindings.QuiclyWriteStream(bindings.Size_t(s.id), bindings.Size_t(id), data, bindings.Size_t(len(data)))
				if errcode != errors.QUICLY_OK {
					s.Logger.Error().Msgf("%v quicly errorcode: %d", s.id, errcode)
					continue
				}
			}

			s.flushOutgoingQueue()
			break
		}
	}
}

func (s *QClientSession) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)

	switch ret {
	case bindings.QUICLY_ERROR_NOT_OPEN:
		s.Logger.Error().Msgf("QUICLY Send failed: QUICLY_ERROR_NOT_OPEN", ret)
		return
	default:
		s.Logger.Debug().Msgf("QUICLY Send failed: %d - %v", num_packets, ret)
		return
	case bindings.QUICLY_OK:
		if int(num_packets) == 0 {
			return
		}
		break
	}

	go func() {
		s.exclusiveLock.Lock()
		defer s.exclusiveLock.Unlock()

		for i := 0; i < int(num_packets); i++ {
			packets_buf[i].Deref() // realize the struct copy from C -> go

			data := bindings.IovecToBytes(packets_buf[i])

			_ = s.Conn.SetWriteDeadline(time.Now().Add(1 * time.Millisecond))

			n, err := s.Conn.Write(data)
			s.Logger.Debug().Msgf("[%v] SEND packet %d bytes [%v]", s.id, n, err)
		}

		runtime.KeepAlive(num_packets)
		runtime.KeepAlive(packets_buf)
	}()
}

// --- Session interface --- //

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) OpenStream() types.Stream {
	s.exclusiveLock.Lock()
	defer s.exclusiveLock.Unlock()

	s.waitStreamOpenLock.Lock()

	if err := s.connect(); err != errors.QUICLY_OK {
		s.Logger.Error().Msgf("connect error: %d", err)
		return nil
	}

	var ptr_id bindings.Size_t = 0

	if ret := bindings.QuiclyOpenStream(bindings.Size_t(s.id), &ptr_id); ret != errors.QUICLY_OK {
		s.Logger.Debug().Msgf("open stream err")
		return nil
	}

	streamId := uint64(ptr_id)

	st := &QStream{
		session: s,
		conn:    s.Conn,
		id:      streamId,
		Logger:  s.Logger,
	}
	st.init()

	s.enterCritical(false)
	s.streams[streamId] = st
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
	if s.OnStreamOpenCallback == nil {
		return
	}

	s.waitStreamOpenLock.Unlock()

	s.enterCritical(true)
	st, ok := s.streams[streamId]
	s.exitCritical(true)
	if ok {
		s.OnStreamOpenCallback(st)
		st.OnOpened()
	}
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	s.Logger.Debug().Msgf(">> On close stream: %d\n", streamId)
	defer s.Logger.Debug().Msgf("<< On close stream: %d\n", streamId)

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

	if !shouldTerm {
		return
	}
	//go func() {
	//	<-time.After(3 * time.Second)
	//	s.enterCritical(false)
	//	shouldTermNow := len(s.streams) == 0
	//	s.exitCritical(false)
	//	if shouldTermNow {
	//		s.Logger.Debug().Msgf(">> Closing parent: %d\n", s.id)
	//		s.Close()
	//	}
	//}()
}

// --- Listener interface --- //

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	s.enterCritical(false)
	if !s.connected || s == nil || s.Conn == nil {
		s.exitCritical(false)
		return nil
	}
	s.Logger.Debug().Msgf("== Connections %v WaitEnd ==\"", s.id)
	defer s.Logger.Debug().Msgf("== Connections %v End ==\"", s.id)

	s.connected = false
	s.ctxCancel()
	// copy of stream list is to workaround lock issues
	for _, stream := range s.streams {
		s.Logger.Warn().Msgf(">> Trying to close stream %d / %p", stream.ID(), stream)
		stream.Close()
		s.Logger.Warn().Msgf(">> Closed stream %d / %p", stream.ID(), stream)
	}
	s.Logger.Warn().Msgf(">> Close queues %d", s.id)
	close(s.incomingQueue)
	close(s.outgoingQueue)
	_ = s.Conn.Close()
	s.exitCritical(false)

	s.Logger.Warn().Msgf(">> Wait routines %d", s.id)
	s.handlersWaiter.Wait()

	if s.OnConnectionClose != nil {
		s.Logger.Debug().Msgf("Close connection: %d\n", s.id)

		s.OnConnectionClose(s)
	}
	bindings.RemoveConnection(s.id)
	return nil
}

func (s *QClientSession) Addr() net.Addr {
	return s.Conn.RemoteAddr()
}
