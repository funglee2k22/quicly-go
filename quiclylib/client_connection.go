package quiclylib

import (
	"context"
	"net"
)

type QClientSession struct {
	Conn *net.UDPConn
	Ctx  context.Context
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

func (s *QClientSession) OpenStream() Stream {
	st := &QStream{
		session: s,
		conn:    s.Conn,
	}

	return st
}

var _ net.Listener = &QClientSession{}
