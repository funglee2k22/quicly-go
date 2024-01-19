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
	streamInBuf    *bytes.Buffer
	outBufferLock  sync.Mutex
	streamOutBuf   *bytes.Buffer

	readDeadline  time.Time
	writeDeadline time.Time

	lastWrite time.Time

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
	if s.streamInBuf == nil {
		s.streamInBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
		s.streamOutBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
		s.bufferUpdateCh = make(chan struct{}, 512)
		s.closed.Store(false)
	}
}

func (s *QStream) ID() uint64 {
	return s.id
}

func (s *QStream) IsClosed() bool {
	val := s.closed.Load()
	if val == nil {
		return true
	}
	return val.(bool)
}

var zeroTime = time.Time{}

func (s *QStream) Read(b []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Info().Msgf("STREAM CLOSED %d", s.id)
		return 0, io.ErrClosedPipe
	}

	if s.readDeadline.IsZero() {
		s.readDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.readDeadline = zeroTime
	}()

	total := 0

	s.inBufferLock.Lock()
	if s.streamInBuf.Len() > 0 {
		wr, _ := s.streamInBuf.Read(b[total:])
		total += wr
		//s.Logger.Info().Msgf("STREAM READ %d BUFF READ: %d / %d", s.id, wr, cap(b))
		if total == cap(b) {
			s.inBufferLock.Unlock()
			return total, nil
		}
	}
	s.inBufferLock.Unlock()

	for !s.IsClosed() {
		select {
		case <-s.bufferUpdateCh:
			s.inBufferLock.Lock()
			wr, _ := s.streamInBuf.Read(b[total:])
			s.inBufferLock.Unlock()
			total += wr
			//s.Logger.Info().Msgf("STREAM READ %d BUFF READ: %d / %d", s.id, wr, cap(b))
			if total == cap(b) {
				return total, nil
			}
			continue

		case <-time.After(time.Until(s.readDeadline)):
			s.inBufferLock.Lock()
			wr, _ := s.streamInBuf.Read(b[total:])
			s.inBufferLock.Unlock()
			total += wr
			//s.Logger.Info().Msgf("STREAM READ %d BUFF READ: %d / %d", s.id, wr, cap(b))
			return total, nil
		}
	}

	return 0, nil
}

func (s *QStream) Write(b []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Error().Msgf("STREAM OUT %d CLOSE", s.id)
		return 0, io.ErrClosedPipe
	}

	if s.writeDeadline.IsZero() {
		s.writeDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.writeDeadline = zeroTime
	}()

	s.outBufferLock.Lock()
	wr, _ := s.streamOutBuf.Write(b)
	s.outBufferLock.Unlock()

	s.Logger.Info().Msgf("STREAM OUT %d: %d / %d", s.id, wr, s.streamOutBuf.Len())
	s.lastWrite = time.Now()

	pkt := &types.Packet{
		Streamid: s.id,
		DataLen:  len(b),
	}
	s.session.StreamPacket(pkt)

	return wr, nil
}

func (s *QStream) Close() error {
	if s.IsClosed() {
		s.Logger.Error().Msgf("STREAM %d CLOSED", s.id)
		return nil
	}
	s.Logger.Info().Msgf("STREAM CLOSE %d", s.id)
	s.closed.Store(true)
	bindings.QuiclyCloseStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), int32(0))
	return nil
}

func (s *QStream) OnOpened() {
	s.Logger.Info().Msgf("STREAM OPEN %d", s.id)
}

func (s *QStream) OnClosed() error {
	s.Logger.Info().Msgf("STREAM ON CLOSED %d", s.id)
	s.closed.Store(true)
	return nil
}

var receivedCounter = 0

func (s *QStream) OnReceived(data []byte, dataLen int) {
	s.init()

	if dataLen == 0 {
		s.Logger.Info().Msgf("STREAM IN %d EMPTY", s.id)
		return
	}
	if s.IsClosed() {
		s.Logger.Error().Msgf("STREAM IN %d CLOSE", s.id)
		return
	}

	s.inBufferLock.Lock()
	s.streamInBuf.Write(data[:dataLen])
	s.inBufferLock.Unlock()

	s.Logger.Info().Msgf("[%v] BUFFER (%d/%d)", s.id, s.streamInBuf.Len(), READ_SIZE)

	receivedCounter++

	select {
	case s.bufferUpdateCh <- struct{}{}:
		break
	case <-time.After(100 * time.Millisecond):
		break
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
