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
		go s.channelsWatcher()
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

func (s *QStream) channelsWatcher() {
	if s.Logger.GetLevel() < log.DebugLevel {
		return
	}
	for !s.IsClosed() {
		<-time.After(5 * time.Millisecond)
		s.Logger.Debug().Msgf("[stream:%v] in:%d buf:%d", s.id, len(s.bufferUpdateCh), s.streamInBuf.Len())
	}
}

func (s *QStream) Read(b []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		return 0, io.ErrClosedPipe
	}

	if s.readDeadline.IsZero() {
		s.readDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.readDeadline = zeroTime
	}()

	select {
	case <-s.bufferUpdateCh:
		s.inBufferLock.Lock()
		wr, _ := s.streamInBuf.Read(b)
		s.Logger.Info().Msgf("STREAM READ %d BUFF READ: %d", s.id, wr)
		s.inBufferLock.Unlock()
		return wr, nil

	case <-time.After(time.Until(s.readDeadline)):
		s.inBufferLock.Lock()
		wr, _ := s.streamInBuf.Read(b)
		s.Logger.Info().Msgf("STREAM READ %d BUFF READ: %d", s.id, wr)
		s.inBufferLock.Unlock()
		if wr > 0 {
			return wr, nil
		}

		if s.IsClosed() && s.streamInBuf.Len() == 0 {
			s.Logger.Debug().Msgf("STREAM READ %d CLOSE", s.id)
			return 0, io.EOF
		}
		s.Logger.Debug().Msgf("STREAM READ %d TIMEOUT", s.id)
		return 0, nil
	}
}

func (s *QStream) Write(b []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Debug().Msgf("STREAM OUT %d CLOSE", s.id)
		return 0, io.ErrClosedPipe
	}

	if s.writeDeadline.IsZero() {
		s.writeDeadline = time.Now().Add(3 * time.Second)
	}
	defer func() {
		s.writeDeadline = zeroTime
	}()

	s.Logger.Info().Msgf("IN")
	s.outBufferLock.Lock()
	wr, _ := s.streamOutBuf.Write(b)
	s.Logger.Info().Msgf("STREAM WRITE %d: %d", s.id, len(b))
	s.outBufferLock.Unlock()
	s.Logger.Info().Msgf("OUT")

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
		return nil
	}
	s.closed.Store(true)
	bindings.QuiclyCloseStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), int32(0))
	return nil
}

func (s *QStream) OnOpened() {
	s.Logger.Info().Msgf("STREAM OPEN %d", s.id)
}

func (s *QStream) OnClosed() error {
	s.Logger.Info().Msgf("STREAM CLOSE %d", s.id)
	s.closed.Store(true)
	return nil
}

var receivedCounter = 0

func (s *QStream) OnReceived(data []byte, dataLen int) {
	s.init()

	if s.IsClosed() || dataLen == 0 {
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
