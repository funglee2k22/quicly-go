package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
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
	id      uint64
	started bool

	streams           map[uint64]types.Stream
	streamsLock       sync.RWMutex
	streamAcceptQueue []types.Stream

	incomingQueue []packet
	inLock        sync.Mutex

	outgoingQueue []packet
	outLock       sync.Mutex

	dataQueue []packet
	dataLock  sync.Mutex
}

func (s *QServerSession) ID() uint64 {
	return s.id
}

func (s *QServerSession) connectionInHandler() {
	var buff = make([]byte, BUF_SIZE)
	s.incomingQueue = make([]packet, 0, 512)
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		n, addr, err := s.Conn.ReadFromUDP(buff)
		if n == 0 || err != nil {
			<-time.After(1 * time.Millisecond)
			continue
		}

		s.inLock.Lock()
		s.Logger.Info().Msgf("IN packet from %v", addr.String())
		s.incomingQueue = append(s.incomingQueue, packet{
			data:    buff[:n],
			dataLen: n,
			addr:    addr,
		})
		buff = make([]byte, BUF_SIZE)
		s.inLock.Unlock()
	}
}

func (s *QServerSession) connectionProcessHandler() {
	s.outgoingQueue = make([]packet, 0, 512)

	var num_packets = bindings.Size_t(0)
	var packets_buf = make([]bindings.Iovec, 10)

	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		s.outLock.Lock()
		if len(s.incomingQueue) == 0 {
			s.outLock.Unlock()
			<-time.After(1 * time.Millisecond)
			continue
		}

		pkt := s.incomingQueue[0]
		s.incomingQueue = s.incomingQueue[1:]
		s.Logger.Info().Msgf("PROCESS packet from %v", pkt.addr.String())
		s.outLock.Unlock()

		returnAddr := pkt.addr
		addr, port := pkt.Address()

		var ptr_id bindings.Size_t = 0

		if bindings.QuiclyProcessMsg(addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id) != bindings.QUICLY_OK {
			continue
		}

		s.id = uint64(ptr_id)
		bindings.RegisterConnection(s, s.id)

		num_packets = bindings.Size_t(10)
		packets_buf = make([]bindings.Iovec, 10)

		var ret = bindings.QuiclyOutgoingMsgQueue(ptr_id, packets_buf, &num_packets)
		if ret != bindings.QUICLY_OK {
			continue
		}

		s.outLock.Lock()
		for i := 0; i < int(num_packets); i++ {
			packets_buf[i].Deref() // realize the struct copy from C -> go

			s.Logger.Info().Msgf("OUT packet to %s:%d, of %d bytes", addr, port, packets_buf[i].Iov_len)

			s.outgoingQueue = append(s.outgoingQueue, packet{
				data:    packets_buf[i].Iov_base,
				dataLen: int(packets_buf[i].Iov_len),
				addr:    returnAddr,
			})
		}
		s.outLock.Unlock()
	}
}

func (s *QServerSession) connectionOutHandler() {
	var msgQueue = make([]packet, 0, 512)
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		s.outLock.Lock()
		if len(s.outgoingQueue) == 0 {
			s.outLock.Unlock()
			<-time.After(1 * time.Millisecond)
			continue
		}

		var backupQueue = s.outgoingQueue
		s.outgoingQueue = msgQueue
		msgQueue = backupQueue
		s.outLock.Unlock()

		for len(msgQueue) >= 1 {
			s.Logger.Info().Msgf("SEND packet of len %d to %v", msgQueue[0].dataLen, msgQueue[0].addr.String())
			s.Conn.WriteToUDP(msgQueue[0].data, msgQueue[0].addr)
			msgQueue = msgQueue[1:]
		}
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
	go s.connectionOutHandler()
	s.started = true
}

func (s *QServerSession) enqueueOutgoingPacket(newPacket packet) {
	s.outLock.Lock()
	s.outgoingQueue = append(s.outgoingQueue, packet{
		data:    newPacket.data,
		dataLen: newPacket.dataLen,
		addr:    nil,
	})
	s.outLock.Unlock()
}

func (s *QServerSession) Accept() (net.Conn, error) {
	s.start()

	for {
		select {
		case <-time.After(1 * time.Millisecond):
			continue
		default:
		}

		s.streamsLock.RLock()
		if len(s.streamAcceptQueue) > 0 {
			s.streamsLock.RUnlock()
			s.streamsLock.Lock()
			defer s.streamsLock.Unlock()

			st := s.streamAcceptQueue[0]
			s.streamAcceptQueue = s.streamAcceptQueue[1:]
			return st, nil
		}
		s.streamsLock.RUnlock()
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
