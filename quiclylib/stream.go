package quiclylib

import "C"
import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	qerrors "github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type QStream struct {
	id      uint64
	session types.Session
	conn    net.Conn

	closed atomic.Value

	inBufferLock sync.Mutex
	streamBuf    *bytes.Buffer

	readDeadline  time.Time
	writeDeadline time.Time

	Logger log.Logger
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

func (s *QStream) IsClosed() bool {
	return s.closed.Load().(bool)
}

func (s *QStream) Read(b []byte) (n int, err error) {
	if s.readDeadline.IsZero() {
		s.readDeadline = time.Now().Add(30 * time.Second)
	}
	defer func() {
		s.readDeadline = time.Time{}
	}()

	if s.streamBuf == nil {
		s.streamBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
	}

	for !time.Now().After(s.readDeadline) {
		s.inBufferLock.Lock()
		if s.streamBuf.Len() > 0 {
			wr, _ := s.streamBuf.Read(b)
			s.inBufferLock.Unlock()
			return wr, nil
		}
		s.inBufferLock.Unlock()

		<-time.After(1 * time.Millisecond)
	}

	if s.IsClosed() && s.streamBuf.Len() == 0 {
		s.Logger.Debug().Msgf("STREAM IN %d CLOSE", s.id)
		return 0, io.EOF
	}
	s.Logger.Debug().Msgf("STREAM IN %d TIMEOUT", s.id)
	return 0, nil
}

func (s *QStream) Write(b []byte) (n int, err error) {
	if s.IsClosed() {
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
	if s.IsClosed() {
		s.Logger.Debug().Msgf("STREAM OUT %d CLOSE", s.id)
		return len(b), io.EOF
	}
	if time.Now().After(s.writeDeadline) {
		s.Logger.Debug().Msgf("STREAM OUT %d TIMEOUT: %d", s.id, len(b))
		return len(b), nil
	}
	s.Logger.Debug().Msgf("STREAM OUT %d: %d", s.id, len(b))
	return len(b), nil
}

func (s *QStream) Close() error {
	if s.IsClosed() {
		return nil
	}
	s.Logger.Debug().Msgf("STREAM CLOSE %d", s.id)
	s.closed.Store(true)
	bindings.QuiclyCloseStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), int32(0))
	return nil
}

func (s *QStream) OnOpened() {}

func (s *QStream) OnClosed() error {
	return s.Close()
}

var receivedCounter = 0

func (s *QStream) OnReceived(data []byte, dataLen int) {
	if s.IsClosed() || dataLen == 0 {
		return
	}
	if s.streamBuf == nil {
		s.streamBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
	}

	s.inBufferLock.Lock()
	defer s.inBufferLock.Unlock()

	s.streamBuf.Write(data[:dataLen])
	s.Logger.Debug().Msgf("[%v] BUFFER (%d/%d)", s.id, s.streamBuf.Len(), READ_SIZE)

	receivedCounter++
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
