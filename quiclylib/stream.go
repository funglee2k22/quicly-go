package quiclylib

import "C"
import (
	"fmt"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"io"
	"net"
	"sync"
	"time"
)

type QStream struct {
	id      uint64
	session types.Session
	conn    net.Conn

	packetInQueue []packet
	inQueueLock   sync.Mutex
}

func (s *QStream) ID() uint64 {
	return s.id
}

func (s *QStream) Read(b []byte) (n int, err error) {
	s.inQueueLock.Lock()
	defer s.inQueueLock.Unlock()

	if len(s.packetInQueue) > 0 {
		data, lenData := s.packetInQueue[0].data, s.packetInQueue[0].dataLen
		s.packetInQueue = s.packetInQueue[1:]
		copy(b, data)
		return lenData, nil
	}
	return 0, io.EOF
}

func (s *QStream) Write(b []byte) (n int, err error) {
	if err := bindings.QuiclyWriteStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), b, bindings.Size_t(len(b))); err == errors.QUICLY_OK {
		return len(b), nil
	}
	return -1, io.EOF
}

func (s *QStream) Close() error {
	return nil
}

func (s *QStream) OnReceived(data []byte, dataLen int) {
	fmt.Println(">>> received")
	if s.packetInQueue == nil {
		s.packetInQueue = make([]packet, 0, 128)
	}

	s.inQueueLock.Lock()
	defer s.inQueueLock.Unlock()

	buf := make([]byte, dataLen)
	copy(buf, data)
	s.packetInQueue = append(s.packetInQueue, packet{
		data:    buf,
		dataLen: dataLen,
		addr:    nil,
	})
}

func (s *QStream) LocalAddr() net.Addr {
	if s.conn != nil {
		return nil
	}
	return s.conn.LocalAddr()
}

func (s *QStream) RemoteAddr() net.Addr {
	if s.conn != nil {
		return nil
	}
	return s.conn.RemoteAddr()
}

func (s *QStream) SetDeadline(t time.Time) error {
	if s.conn != nil {
		return nil
	}
	return s.conn.SetDeadline(t)
}

func (s *QStream) SetReadDeadline(t time.Time) error {
	if s.conn != nil {
		return nil
	}
	return s.conn.SetReadDeadline(t)
}

func (s *QStream) SetWriteDeadline(t time.Time) error {
	if s.conn != nil {
		return nil
	}
	return s.conn.SetWriteDeadline(t)
}

var _ net.Conn = &QStream{}
