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

	OutgoingQueue chan *packet
	incomingQueue chan *packet
}

func (s *QClientSession) init() {
	if s.incomingQueue == nil {
		s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

		s.incomingQueue = make(chan *packet, 1024)
		s.OutgoingQueue = make(chan *packet, 1024)
		s.streams = make(map[uint64]types.Stream)

		go s.channelsWatcher()
	}
}

func (s *QClientSession) channelsWatcher() {
	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
			break
		}
		s.Logger.Info().Msgf("[conn:%v] in:%d out:%d str:%d", s.id, len(s.incomingQueue), len(s.OutgoingQueue), len(s.streams))
	}
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

		s.Logger.Debug().Msgf("[%v] UDP packet", s.id)
		n, addr, err := s.Conn.ReadFromUDP(buffList[0])
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

		pkt := &packet{
			data:    buf[:n],
			dataLen: n,
			addr:    addr,
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
			if pkt == nil {
				break
			}
			addr, port := pkt.Address()
			if len(addr) == 0 || port == -1 {
				addr, port = returnAddr.IP.String(), returnAddr.Port
			}

			var ptr_id bindings.Size_t = 0

			err := bindings.QuiclyProcessMsg(int32(1), addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id)
			if err != bindings.QUICLY_OK {
				if err == bindings.QUICLY_ERROR_PACKET_IGNORED {
					s.Logger.Error().Msgf("[%v] Process error %d bytes (ignored processing %v)", s.id, pkt.dataLen, err)
				} else {
					s.Logger.Error().Msgf("[%v] Received %d bytes (failed processing %v)", s.id, pkt.dataLen, err)
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

	for {
		select {
		case pkt := <-s.OutgoingQueue:
			if pkt == nil {
				continue
			}
			//s.Logger.Info().Msgf("STREAM WRITE %d: %d - %v", pkt.streamid, pkt.dataLen, pkt.data[:pkt.dataLen])

			s.streamsLock.RLock()
			var stream, _ = s.streams[pkt.streamid].(*QStream)
			s.streamsLock.RUnlock()

			stream.outBufferLock.Lock()
			data := append([]byte{}, stream.streamOutBuf.Bytes()...)
			s.Logger.Info().Msgf("STREAM WRITE %d: %d", pkt.streamid, len(data))

			stream.streamOutBuf.Reset()
			stream.outBufferLock.Unlock()

			errcode := bindings.QuiclyWriteStream(bindings.Size_t(s.id), bindings.Size_t(pkt.streamid), data, bindings.Size_t(len(data)))
			if errcode != errors.QUICLY_OK {
				s.Logger.Error().Msgf("%v quicly errorcode: %d", s.id, errcode)
				continue
			}
			continue

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

		n, err := s.Conn.Write(data)
		s.Logger.Debug().Msgf("SEND packet of len %d [%v]", n, err)
	}
}

func (s *QClientSession) ID() uint64 {
	return s.id
}

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
	close(s.OutgoingQueue)
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

func (s *QClientSession) OpenStream() types.Stream {
	if s.connect() != errors.QUICLY_OK {
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
	st.closed.Store(false)

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
	s.Logger.Info().Msgf("On close stream: %d\n", streamId)

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

var _ net.Listener = &QClientSession{}

var _ types.Session = &QClientSession{}
