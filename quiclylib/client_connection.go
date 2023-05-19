package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"net"
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
	id        uint64
	connected bool

	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	incomingQueue []packet
	inLock        sync.Mutex

	outgoingQueue []packet
	outLock       sync.Mutex

	dataQueue []packet
	dataLock  sync.Mutex
}

func (s *QClientSession) connectionInHandler() {
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

func (s *QClientSession) connectionProcessHandler() {
	s.outgoingQueue = make([]packet, 0, 512)

	var num_packets = bindings.Size_t(0)
	var packets_buf = make([]bindings.Iovec, 10)

	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		var returnAddr *net.UDPAddr = nil

		s.outLock.Lock()
		if len(s.incomingQueue) == 0 {
			s.outLock.Unlock()
			retAddr := s.Conn.RemoteAddr()
			returnAddr = retAddr.(*net.UDPAddr)

		} else {
			pkt := s.incomingQueue[0]
			s.incomingQueue = s.incomingQueue[1:]
			s.outLock.Unlock()

			returnAddr = pkt.addr
			addr, port := pkt.Address()

			var ptr_id bindings.Size_t = 0

			if bindings.QuiclyProcessMsg(int32(1), addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id) != bindings.QUICLY_OK {
				continue
			}

			s.id = uint64(ptr_id)
			bindings.RegisterConnection(s, s.id)
		}

		num_packets = bindings.Size_t(10)
		packets_buf = make([]bindings.Iovec, 10)

		var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)
		if ret != bindings.QUICLY_OK || num_packets == 0 {
			continue
		}

		addr, port := returnAddr.IP.String(), returnAddr.Port

		s.outLock.Lock()
		for i := 0; i < int(num_packets); i++ {
			packets_buf[i].Deref() // realize the struct copy from C -> go

			s.Logger.Info().Msgf("OUT packet to %s:%d, of %d bytes", addr, port, packets_buf[i].Iov_len)

			s.outgoingQueue = append(s.outgoingQueue, packet{
				data:    packets_buf[i].Iov_base,
				dataLen: int(packets_buf[i].Iov_len),
				addr:    nil,
			})
		}
		s.outLock.Unlock()
	}
}

func (s *QClientSession) connectionOutHandler() {
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
			n, err := s.Conn.Write(msgQueue[0].data)
			s.Logger.Info().Msgf("SEND packet of len %d to %v [%d:%v]", msgQueue[0].dataLen, msgQueue[0].addr.String(), n, err)
			msgQueue = msgQueue[1:]
		}
	}
}

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	if !s.connected || s.Conn == nil {
		return nil
	}
	if s.OnConnectionClose != nil {
		s.OnConnectionClose(s)
	}
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

	s.streams = make(map[uint64]types.Stream)

	var ptr_id bindings.Size_t = 0

	udpAddr := s.Addr().(*net.UDPAddr)

	if ret := bindings.QuiclyConnect(udpAddr.IP.String(), int32(udpAddr.Port), &ptr_id); ret != errors.QUICLY_OK {
		return int(ret)
	}

	s.id = uint64(ptr_id)
	bindings.RegisterConnection(s, s.id)

	s.connected = true

	go s.connectionInHandler()
	go s.connectionProcessHandler()
	go s.connectionOutHandler()

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
	}
	s.streamsLock.Lock()
	s.streams[streamId] = st
	s.streamsLock.Unlock()

	return st
}

func (s *QClientSession) GetStream(id uint64) types.Stream {
	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	return s.streams[id]
}

func (s *QClientSession) OnStreamOpen(streamId uint64) {
	if s.OnStreamOpenCallback == nil {
		return
	}

	s.streamsLock.Lock()
	st, ok := s.streams[streamId]
	s.streamsLock.Unlock()
	if ok {
		s.OnStreamOpenCallback(st)
	}
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	if s.OnStreamCloseCallback == nil {
		return
	}

	s.streamsLock.Lock()
	st, ok := s.streams[streamId]
	s.streamsLock.Unlock()
	if ok {
		s.OnStreamCloseCallback(st, error)
	}
}

var _ net.Listener = &QClientSession{}
