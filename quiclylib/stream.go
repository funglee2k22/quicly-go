package quiclylib

import "C"
import (
	"errors"
	"fmt"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	qerrors "github.com/Project-Faster/quicly-go/quiclylib/errors"
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
	closed  bool

	packetInQueue []packet
	inQueueLock   sync.Mutex

	readDeadline  time.Time
	writeDeadline time.Time
}

type timeoutErrorType struct{}

func (e *timeoutErrorType) Error() string {
	return "stream timed-out"
}

func (e *timeoutErrorType) Timeout() bool {
	return true
}

var timeoutError = &timeoutErrorType{}

func (s *QStream) ID() uint64 {
	return s.id
}

func (s *QStream) Read(b []byte) (n int, err error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.readDeadline.IsZero() {
		s.readDeadline = time.Now().Add(30 * time.Second)
	}
	defer func() {
		s.readDeadline = time.Time{}
	}()

	for !time.Now().After(s.readDeadline) {
		s.inQueueLock.Lock()
		if len(s.packetInQueue) > 0 {
			data, lenData := s.packetInQueue[0].data, s.packetInQueue[0].dataLen
			s.packetInQueue = s.packetInQueue[1:]
			s.inQueueLock.Unlock()

			copy(b, data)
			return lenData, nil
		}
		s.inQueueLock.Unlock()
		time.After(1 * time.Millisecond)
	}
	return 0, timeoutError
}

func (s *QStream) Write(b []byte) (n int, err error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.writeDeadline.IsZero() {
		s.writeDeadline = time.Now().Add(30 * time.Second)
	}
	defer func() {
		s.writeDeadline = time.Time{}
	}()
	errcode := bindings.QuiclyWriteStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), b, bindings.Size_t(len(b)))
	if errcode != qerrors.QUICLY_OK {
		return 0, errors.New(fmt.Sprintf("quicly errorcode: %d", errcode))
	}
	if time.Now().After(s.writeDeadline) {
		return len(b), timeoutError
	}
	return len(b), nil
}

func (s *QStream) Close() error {
	bindings.QuiclyCloseStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), int32(0))
	s.closed = true
	return nil
}

func (s *QStream) OnReceived(data []byte, dataLen int) {
	if s.closed {
		return
	}
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
	s.readDeadline = t
	s.writeDeadline = t
	return nil
}

func (s *QStream) SetReadDeadline(t time.Time) error {
	s.readDeadline = t
	return nil
}

func (s *QStream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline = t
	return nil
}

var _ net.Conn = &QStream{}
