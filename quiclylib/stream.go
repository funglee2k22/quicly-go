package quiclylib

import "C"
import (
	"bytes"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
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
	sentBytesCh    chan uint64

	inBufferLock  sync.Mutex
	streamInBuf   *bytes.Buffer
	outBufferLock sync.Mutex
	streamOutBuf  *bytes.Buffer

	readDeadline  time.Time
	writeDeadline time.Time

	totalWrite   uint64
	totalRead    uint64
	writtenBytes uint64

	sentBytes  uint64
	ackedBytes uint64

	Logger log.Logger
}

func (s *QStream) init() {
	if s.streamInBuf == nil {
		s.streamInBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
		s.streamOutBuf = bytes.NewBuffer(make([]byte, 0, READ_SIZE))
		s.bufferUpdateCh = make(chan struct{}, 512)
		s.sentBytesCh = make(chan uint64, 512)
		s.closed.Store(false)

		go s.flushToStream()
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

func (s *QStream) Read(buffRd []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Debug().Msgf("[%d] QSTREAM CLOSED", s.id)
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
		wr, _ := s.streamInBuf.Read(buffRd[total:])
		total += wr
		s.totalRead += uint64(wr)
		s.Logger.Debug().Msgf("QSTREAM READ %d BUFF READ 1: %d / %d / %d", s.id, total, wr, cap(buffRd))
		if total >= cap(buffRd) {
			s.inBufferLock.Unlock()
			return total, nil
		}
	}
	s.inBufferLock.Unlock()

	for !s.IsClosed() {
		select {
		case <-s.bufferUpdateCh:
			s.inBufferLock.Lock()
			wr, _ := s.streamInBuf.Read(buffRd[total:])
			s.inBufferLock.Unlock()
			total += wr
			s.totalRead += uint64(wr)
			s.Logger.Debug().Msgf("QSTREAM READ %d BUFF READ 2: %d / %d / %d", s.id, total, wr, cap(buffRd))
			if total == cap(buffRd) {
				return total, nil
			}
			continue

		case <-time.After(time.Until(s.readDeadline)):
			s.inBufferLock.Lock()
			wr, _ := s.streamInBuf.Read(buffRd[total:])
			s.inBufferLock.Unlock()
			total += wr
			s.totalRead += uint64(wr)
			s.Logger.Debug().Msgf("QSTREAM READ %d BUFF READ 3: %d / %d / %d", s.id, wr, total, cap(buffRd))
			return total, timeoutError
		}
	}

	return 0, io.ErrClosedPipe
}

func (s *QStream) Write(buffWr []byte) (n int, err error) {
	s.init()

	if s.IsClosed() {
		s.Logger.Debug().Msgf("[%d] QSTREAM OUT CLOSE", s.id)
		return 0, io.ErrClosedPipe
	}

	defer func() {
		s.totalWrite += uint64(n)
	}()

	s.Logger.Debug().Msgf("[%v] SEND packet %d bytes [%v]", s.id, len(buffWr), s.ID())
	defer s.Logger.Debug().Msgf("[%d] QSTREAM OUT", s.id)

	s.outBufferLock.Lock()
	_, _ = s.streamOutBuf.Write(buffWr)
	s.writtenBytes += uint64(n)
	s.outBufferLock.Unlock()

	return int(<-s.sentBytesCh), nil
}

func (s *QStream) flushToStream() {
	for !s.IsClosed() {
		<-time.After(200 * time.Millisecond)

		s.outBufferLock.Lock()
		data := append([]byte{}, s.streamOutBuf.Bytes()...)
		waitSize := len(data)
		s.streamOutBuf.Reset()
		s.outBufferLock.Unlock()

		errcode := bindings.QuiclyWriteStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id),
			data, bindings.Size_t(waitSize))

		s.Logger.Debug().Msgf("%v quicly write flush: %d", s.session.ID(), waitSize)

		if errcode != errors.QUICLY_OK {
			s.Logger.Error().Msgf("%v quicly errorcode: %d", s.session.ID(), errcode)
			continue
		}

		sent, err := s.waitSentBytes(uint64(waitSize))
		if err != nil {
			s.Logger.Error().Msgf("%v quicly errorcode: %d", s.session.ID(), errcode)
		}
		s.sentBytesCh <- sent
	}
}

func (s *QStream) Close() error {
	if s.IsClosed() {
		s.Logger.Error().Msgf("QSTREAM %d CLOSED", s.id)
		return nil
	}
	bindings.QuiclyCloseStream(bindings.Size_t(s.session.ID()), bindings.Size_t(s.id), int32(0))
	s.Logger.Info().Msgf("[%d] QSTREAM CLOSE [read:%v,write:%d]", s.id, s.totalRead, s.totalWrite)
	return nil
}

func (s *QStream) OnOpened() {
	s.Logger.Info().Msgf("[%d] QSTREAM OPEN", s.id)
}

func (s *QStream) OnClosed() error {
	s.Logger.Debug().Msgf("[%d] QSTREAM ON CLOSED", s.id)
	s.closed.Store(true)
	return nil
}

func (s *QStream) OnSentBytes(size uint64) {
	s.sentBytes += size
	s.Logger.Debug().Msgf("[%d] QSTREAM SENT %v bytes (%v)", s.id, size, s.sentBytes)
}

func (s *QStream) OnAckedSentBytes(size uint64) {
	s.ackedBytes += size
	s.Logger.Debug().Msgf("[%d] QSTREAM ACKED %v bytes (%v)", s.id, size, s.ackedBytes)
}

var receivedCounter = 0

func (s *QStream) OnReceived(data []byte, dataLen int) {
	s.init()

	if dataLen == 0 {
		s.Logger.Debug().Msgf("[%d] QSTREAM IN EMPTY", s.id)
		return
	}
	if s.IsClosed() {
		s.Logger.Debug().Msgf("[%d] QSTREAM IN CLOSE", s.id)
		return
	}

	s.inBufferLock.Lock()
	s.streamInBuf.Write(data[:dataLen])
	s.inBufferLock.Unlock()

	s.Logger.Debug().Msgf("[%v] BUFFER (%d/%d)", s.id, s.streamInBuf.Len(), READ_SIZE)

	receivedCounter++

	go func() {
		s.bufferUpdateCh <- struct{}{}
	}()
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

func (s *QStream) waitSentBytes(size uint64) (uint64, error) {
	begin := s.sentBytes

	maxCounter := 200
	s.Logger.Debug().Msgf("[%v] SEND START sync (written:%d / sent:%d / acked:%d)", s.id, s.writtenBytes, s.sentBytes, s.ackedBytes)
	for maxCounter > 0 && s.sentBytes < begin+size {

		s.Logger.Debug().Msgf("[%v] SEND STEP sync (written:%d / sent:%d / acked:%d)", s.id, s.writtenBytes, s.sentBytes, s.ackedBytes)
		if s.IsClosed() {
			s.Logger.Debug().Msgf("[%d] QSTREAM OUT CLOSE", s.id)
			return s.sentBytes - begin, io.ErrClosedPipe
		}

		<-time.After(1 * time.Millisecond)
		maxCounter--
	}

	s.Logger.Debug().Msgf("[%v] SEND END sync (written:%d / sent:%d / acked:%d)", s.id, s.writtenBytes, s.sentBytes, s.ackedBytes)
	return s.sentBytes - begin, nil
}

func (s *QStream) Sync() bool {
	return s.writtenBytes > 0 && (s.writtenBytes <= s.ackedBytes)
}

var _ net.Conn = &QStream{}
