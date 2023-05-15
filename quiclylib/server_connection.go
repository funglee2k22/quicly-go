package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"net"
	"sync"
	"time"
)

type QServerSession struct {
	Conn *net.UDPConn
	Ctx  context.Context

	id      uint64
	started bool

	incomingQueue []packet
	inLock        sync.Mutex

	outgoingQueue []packet
	outLock       sync.Mutex

	dataQueue []packet
	dataLock  sync.Mutex
}

func (s *QServerSession) connectionInHandler() {
	var buff = make([]byte, 4096)
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
		s.incomingQueue = append(s.incomingQueue, packet{
			data:    buff[:n],
			dataLen: n,
			addr:    addr,
		})
		buff = make([]byte, 4096)
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
		s.outLock.Unlock()

		addr, port := pkt.Address()

		var ptr_id bindings.Size_t = 0

		if bindings.QuiclyServerProcessMsg(addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id) != bindings.QUICLY_OK {
			continue
		}

		s.id = uint64(ptr_id)

		num_packets = bindings.Size_t(0)

		var ret = bindings.QuiclySendMsg(ptr_id, packets_buf, &num_packets)
		if ret != bindings.QUICLY_OK {
			continue
		}

		s.outLock.Lock()
		for i := 0; i < int(num_packets); i++ {
			s.outgoingQueue = append(s.outgoingQueue, packet{
				data:    packets_buf[i].Iov_base,
				dataLen: int(packets_buf[i].Iov_len),
				addr:    nil,
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
			s.Conn.Write(msgQueue[0].data)
			msgQueue = msgQueue[1:]
		}
	}
}

func (s *QServerSession) start() {
	if s.started {
		return
	}
	go s.connectionInHandler()
	go s.connectionProcessHandler()
	go s.connectionOutHandler()
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

	st := &QStream{
		session: s,
		conn:    s.Conn,
	}
	return st, nil
}

func (s *QServerSession) Close() error {
	if s.Conn == nil {
		return nil
	}
	defer func() {
		s.Conn = nil
	}()
	return s.Conn.Close()
}

func (s *QServerSession) Addr() net.Addr {
	if s.Conn == nil {
		return nil
	}
	return s.Conn.LocalAddr()
}

func (s *QServerSession) OpenStream() Stream {
	return nil
}

var _ net.Listener = &QServerSession{}
