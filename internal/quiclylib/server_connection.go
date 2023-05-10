package quiclylib

import (
	"context"
	"net"
	"sync"
	"time"
)

type QServerSession struct {
	Conn *net.UDPConn
	Ctx  context.Context

	id uint64

	outgoingQueue [][]byte
	outLock       sync.Mutex
}

func (s *QServerSession) connectionInHandler() {
	var buff = make([]byte, 4096)
	s.outgoingQueue = make([][]byte, 0, 512)
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

		if QuiclyProcessMsg(false, addr, buff[:n], &s.id) != QUICLY_OK {
			continue
		}

		var ret, packets = QuiclySendMsg(s.id)
		if ret != QUICLY_OK {
			continue
		}

		s.outLock.Lock()
		s.outgoingQueue = append(s.outgoingQueue, packets...)
		s.outLock.Unlock()
	}
}

func (s *QServerSession) connectionOutHandler() {
	var msgQueue = make([][]byte, 0, 512)
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		s.outLock.Lock()
		if len(s.outgoingQueue) == 0 {
			s.outLock.Unlock()
			continue
		}

		var backupQueue = s.outgoingQueue
		s.outgoingQueue = msgQueue
		msgQueue = backupQueue
		s.outLock.Unlock()

		for len(msgQueue) >= 1 {
			s.Conn.Write(msgQueue[0])
			msgQueue = msgQueue[1:]
		}
	}
}

func (s *QServerSession) Accept() (net.Conn, error) {
	st := &QStream{
		session: s,
		conn:    s.Conn,
	}
	st.init()
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
