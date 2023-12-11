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

	exclusiveLock sync.RWMutex

	outgoingQueue chan *types.Packet
	incomingQueue chan *types.Packet
}

var _ net.Listener = &QClientSession{}
var _ types.Session = &QClientSession{}

func (s *QClientSession) init() {
	if s.incomingQueue == nil {
		s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

		s.incomingQueue = make(chan *types.Packet, 1024)
		s.outgoingQueue = make(chan *types.Packet, 1024)
		s.streams = make(map[uint64]types.Stream)
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

	for i := 0; i < runtime.NumCPU(); i++ {
		s.handlersWaiter.Add(2)
		go s.connectionInHandler()
		go s.connectionWriteHandler()
	}
	s.handlersWaiter.Add(1)
	go s.connectionProcessHandler()

	if s.OnConnectionOpen != nil {
		s.OnConnectionOpen(s)
	}

	return errors.QUICLY_OK
}

func (s *QClientSession) connectionInHandler() {
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	var buffList = make([][]byte, 0, 128)
	for i := 0; i < 128; i++ {
		buffList = append(buffList, make([]byte, READ_SIZE))
	}

	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(1 * time.Millisecond):
			break
		}

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
				buffList = append(buffList, make([]byte, READ_SIZE))
			}
		}

		pkt := &types.Packet{
			Data:       buf[:n],
			DataLen:    n,
			RetAddress: addr,
		}
		select {
		case s.incomingQueue <- pkt:
			break
		case <-time.After(100 * time.Millisecond):
			break
		}
	}
}

func (s *QClientSession) connectionProcessHandler() {
	returnAddr := s.Conn.RemoteAddr().(*net.UDPAddr)
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	for {
		select {
		case pkt := <-s.incomingQueue:
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

		case <-s.Ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
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

	nextDump := time.Now().Add(100 * time.Millisecond)

	sleepCounter := 0
	for {
		if sleepCounter > 100 {
			<-time.After(100 * time.Microsecond)
			sleepCounter = 0
		}
		sleepCounter++

		if time.Until(nextDump).Milliseconds() < int64(1) {
			s.streamsLock.RLock()
			for id, sr := range s.streams {
				stream := sr.(*QStream)

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
			s.streamsLock.RUnlock()
			nextDump = nextDump.Add(100 * time.Millisecond)
		}

		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			break
		}

		s.flushOutgoingQueue()
	}
}

func (s *QClientSession) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	s.exclusiveLock.Lock()
	defer s.exclusiveLock.Unlock()

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)

	if ret != bindings.QUICLY_OK {
		s.Logger.Debug().Msgf("QUICLY Send failed: %d - %v", num_packets, ret)
		return
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		_ = s.Conn.SetWriteDeadline(time.Now().Add(2 * time.Millisecond))

		n, err := s.Conn.Write(data)
		s.Logger.Debug().Msgf("SEND packet of len %d [%v]", n, err)
	}
}

// --- Session interface --- //

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) OpenStream() types.Stream {
	if err := s.connect(); err != errors.QUICLY_OK {
		s.Logger.Error().Msgf("connect error: %d", err)
		return nil
	}

	var ptr_id bindings.Size_t = 0

	if ret := bindings.QuiclyOpenStream(bindings.Size_t(s.id), &ptr_id); ret != errors.QUICLY_OK {
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

	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	s.streams[streamId] = st

	return st
}

func (s *QClientSession) GetStream(id uint64) types.Stream {
	s.streamsLock.RLock()
	defer s.streamsLock.RUnlock()
	return s.streams[id]
}

func (s *QClientSession) StreamPacket(packet *types.Packet) {
	if packet == nil {
		return
	}
	s.outgoingQueue <- packet
}

func (s *QClientSession) OnStreamOpen(streamId uint64) {
	if s.OnStreamOpenCallback == nil {
		return
	}

	s.streamsLock.RLock()
	st, ok := s.streams[streamId]
	s.streamsLock.RUnlock()
	if ok {
		s.OnStreamOpenCallback(st)
		st.OnOpened()
	}
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	s.Logger.Debug().Msgf("On close stream: %d\n", streamId)

	if s.OnStreamCloseCallback == nil {
		return
	}

	s.streamsLock.Lock()
	st, ok := s.streams[streamId]
	delete(s.streams, streamId)
	s.streamsLock.Unlock()

	if ok {
		s.OnStreamCloseCallback(st, error)
		_ = st.OnClosed()
	}
}

// --- Listener interface --- //

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	if !s.connected || s == nil || s.Conn == nil {
		return nil
	}
	s.Logger.Info().Msgf("== Connection %v WaitEnd ==\"", s.id)
	defer s.Logger.Info().Msgf("== Connection %v End ==\"", s.id)

	s.ctxCancel()
	close(s.incomingQueue)
	close(s.outgoingQueue)
	_ = s.Conn.Close()

	if s.OnConnectionClose != nil {
		s.Logger.Debug().Msgf("Close connection: %d\n", s.id)
		s.OnConnectionClose(s)
	}
	s.handlersWaiter.Wait()
	bindings.RemoveConnection(s.id)
	return nil
}

func (s *QClientSession) Addr() net.Addr {
	return s.Conn.RemoteAddr()
}
