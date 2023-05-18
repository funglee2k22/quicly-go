package quiclylib

import (
	"context"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"net"
	"sync"
)

type QClientSession struct {
	// exported fields
	Conn   *net.UDPConn
	Ctx    context.Context
	Logger log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id          uint64
	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex
}

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	if s.OnConnectionClose != nil && s.Conn != nil {
		s.OnConnectionClose(s)
	}

	return nil
}

func (s *QClientSession) Addr() net.Addr {
	return nil
}

func (s *QClientSession) OpenStream() types.Stream {
	st := &QStream{
		session: s,
		conn:    s.Conn,
	}

	return st
}

func (s *QClientSession) GetStream(id uint64) types.Stream {
	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	return s.streams[id]
}

func (s *QClientSession) OnStreamOpen(streamId uint64) {
	if s.OnConnectionOpen != nil && len(s.streams) == 1 {
		s.OnConnectionOpen(s)
	}

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
