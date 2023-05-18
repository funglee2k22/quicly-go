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
	Callbacks

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
	if s.OnStreamOpenCallback == nil {
		return
	}

	s.OnStreamOpenCallback(nil)
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	if s.OnStreamCloseCallback != nil {
		s.OnStreamCloseCallback(nil, error)
	}
}

var _ net.Listener = &QClientSession{}
