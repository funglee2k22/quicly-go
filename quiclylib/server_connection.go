package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"io"
	"net"
	"sync"
	"time"

	log "github.com/rs/zerolog"
)

type QServerSession struct {
	// exported fields
	Conn   *net.UDPConn
	Ctx    context.Context
	Logger log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id             uint64
	started        bool
	ctxCancel      context.CancelFunc
	handlersWaiter sync.WaitGroup

	streams           map[uint64]types.Stream
	streamsLock       sync.RWMutex
	streamAcceptQueue []types.Stream

	exclusiveLock sync.Mutex

	incomingQueue chan *packet
	outgoingQueue chan *packet
}

func (s *QServerSession) ID() uint64 {
	return s.id
}

func (s *QServerSession) init() {
	if s.incomingQueue == nil {
		s.Ctx, s.ctxCancel = context.WithCancel(s.Ctx)

		s.incomingQueue = make(chan *packet, 1024)
		s.outgoingQueue = make(chan *packet, 1024)
		s.streams = make(map[uint64]types.Stream)

		go s.channelsWatcher()
	}
}

func (s *QServerSession) channelsWatcher() {
	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
			break
		}
		s.Logger.Info().Msgf("[conn:%v] in:%d out:%d str:%d", s.id, len(s.incomingQueue), len(s.outgoingQueue), len(s.streams))
	}
}

func (s *QServerSession) connectionInHandler() {
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

		s.Logger.Info().Msgf("[%v] UDP packet", s.id)
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

		s.Logger.Info().Msgf("CONN READ %d: %d", s.id, n)
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

func (s *QServerSession) connectionProcessHandler() {
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
			s.Logger.Info().Msgf("CONN PROC %d: %d", pkt.streamid, pkt.dataLen)

			addr, port := pkt.Address()

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

func (s *QServerSession) connectionWriteHandler() {
	defer func() {
		_ = recover()
		s.handlersWaiter.Done()
	}()

	for {
		select {
		case pkt := <-s.outgoingQueue:
			if pkt == nil {
				continue
			}
			s.Logger.Info().Msgf("CONN WRITE %d: %d - %v", pkt.streamid, pkt.dataLen, pkt.data[:pkt.dataLen])

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

func (s *QServerSession) flushOutgoingQueue() {
	num_packets := bindings.Size_t(32)
	packets_buf := make([]bindings.Iovec, 32)

	s.exclusiveLock.Lock()
	defer s.exclusiveLock.Unlock()

	var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)

	if ret != bindings.QUICLY_OK {
		s.Logger.Info().Msgf("QUICLY Send failed: %d - %v", num_packets, ret)
		return
	}

	for i := 0; i < int(num_packets); i++ {
		packets_buf[i].Deref() // realize the struct copy from C -> go

		data := bindings.IovecToBytes(packets_buf[i])

		n, err := s.Conn.Write(data)
		s.Logger.Debug().Msgf("SEND packet of len %d [%v]", n, err)
	}
}

func (s *QServerSession) start() {
	if s.started {
		return
	}
	s.streams = make(map[uint64]types.Stream)
	s.streamAcceptQueue = make([]types.Stream, 0, 32)

	go s.connectionInHandler()
	go s.connectionProcessHandler()
	go s.connectionWriteHandler()
	s.started = true
}

func (s *QServerSession) enqueueOutgoingPacket(newPacket packet) {
	s.outgoingQueue <- &packet{
		data:    newPacket.data,
		dataLen: newPacket.dataLen,
		addr:    nil,
	}
}

func (s *QServerSession) Accept() (net.Conn, error) {
	s.start()

	for {
		select {
		case <-s.Ctx.Done():
			return nil, io.ErrClosedPipe
		case <-time.After(1 * time.Millisecond):
			continue
		default:
		}

		s.streamsLock.RLock()
		checkEmpty := len(s.streamAcceptQueue) == 0
		s.streamsLock.RUnlock()
		if checkEmpty {
			continue
		}

		s.streamsLock.Lock()
		defer s.streamsLock.Unlock()

		st := s.streamAcceptQueue[0]
		s.streamAcceptQueue = s.streamAcceptQueue[1:]
		return st, nil
	}
}

func (s *QServerSession) Close() error {
	if s.Conn == nil {
		return nil
	}
	defer func() {
		s.Conn = nil
		bindings.RemoveConnection(s.id)
		if s.OnConnectionClose != nil {
			s.OnConnectionClose(s)
		}
	}()
	s.streamsLock.Lock()
	for _, stream := range s.streams {
		stream.Close()
	}
	s.streamAcceptQueue = nil
	s.streams = nil
	s.streamsLock.Unlock()
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
	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	return s.streams[id]
}

func (s *QServerSession) OnStreamOpen(streamId uint64) {
	if s.OnConnectionOpen != nil && len(s.streams) == 1 {
		s.OnConnectionOpen(s)
	}

	st := &QStream{
		session: s,
		conn:    s.Conn,
		id:      streamId,
	}
	s.streamsLock.Lock()
	s.streams[streamId] = st
	s.streamAcceptQueue = append(s.streamAcceptQueue, st)
	s.streamsLock.Unlock()

	if s.OnStreamOpenCallback != nil {
		s.OnStreamOpenCallback(st)
	}
}

func (s *QServerSession) OnStreamClose(streamId uint64, error int) {
	s.streamsLock.Lock()
	closedStream := s.streams[streamId]
	s.streams[streamId] = nil
	s.streamsLock.Unlock()

	if closedStream != nil && s.OnStreamCloseCallback != nil {
		s.OnStreamCloseCallback(closedStream, error)
	}
}

var _ net.Listener = &QServerSession{}
