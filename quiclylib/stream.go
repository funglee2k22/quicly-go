package quiclylib

import "C"
import (
	"bytes"
	"github.com/Project-Faster/quicly-go/internal/bindings"
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

	bufferUpdateCh chan struct{}
	inBufferLock   sync.Mutex
	streamBuf      *bytes.Buffer

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

func (s *QStream) init() {
	if s.streamBuf == nil {
		s.streamBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
		s.bufferUpdateCh = make(chan struct{}, 512)
	}
}

func (s *QStream) ID() uint64 {
	return s.id
}

func (s *QStream) IsClosed() bool {
	return s.closed.Load().(bool)
}

var zeroTime = time.Time{}

func (s *QStream) Read(b []byte) (n int, err error) {
	s.init()

	if s.readDeadline.IsZero() {
		s.readDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.readDeadline = zeroTime
	}()

	select {
	case <-s.bufferUpdateCh:
		s.inBufferLock.Lock()
		wr, _ := s.streamBuf.Read(b)
		s.Logger.Debug().Msgf("STREAM IN %d BUFF READ: %d", s.id, wr)
		s.inBufferLock.Unlock()
		return wr, nil

	case <-time.After(time.Until(s.readDeadline)):
		s.inBufferLock.Lock()
		wr, _ := s.streamBuf.Read(b)
		s.Logger.Debug().Msgf("STREAM IN %d BUFF READ: %d", s.id, wr)
		s.inBufferLock.Unlock()
		if wr > 0 {
			return wr, nil
		}

		if s.IsClosed() && s.streamBuf.Len() == 0 {
			s.Logger.Debug().Msgf("STREAM IN %d CLOSE", s.id)
			return 0, io.EOF
		}
		s.Logger.Debug().Msgf("STREAM IN %d TIMEOUT", s.id)
		return 0, nil
	}
}

func (s *QStream) Write(b []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Debug().Msgf("STREAM OUT %d CLOSE", s.id)
		return 0, io.EOF
	}
	if s.writeDeadline.IsZero() {
		s.writeDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.writeDeadline = zeroTime
	}()

	var client, _ = s.session.(*QClientSession)
	pkt := &packet{
		streamid: s.id,
		data:     b,
		dataLen:  len(b),
	}
	select {
	case client.OutgoingQueue <- pkt:
		break
	case <-time.After(1 * time.Millisecond):
		return len(b), timeoutError
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
	s.init()

	if s.IsClosed() || dataLen == 0 {
		return
	}
	s.inBufferLock.Lock()
	s.inBufferLock.Unlock()

	s.streamBuf.Write(data[:dataLen])
	s.Logger.Debug().Msgf("[%v] BUFFER (%d/%d)", s.id, s.streamBuf.Len(), READ_SIZE)

	receivedCounter++

	if s.streamBuf.Len() >= BUF_SIZE {
		select {
		case s.bufferUpdateCh <- struct{}{}:
			break
		case <-time.After(100 * time.Millisecond):
			break
		}
	}
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
