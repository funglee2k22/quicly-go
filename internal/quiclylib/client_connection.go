package quiclylib

import "net"

type QClientSession struct {
	id uint32

	Conn *net.UDPConn
}

func (s *QClientSession) ID() uint32 {
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

func (s *QClientSession) OpenStream() Stream {
	st := &QStream{
		session: s,
		conn:    s.Conn,
	}

	return st
}

var _ net.Listener = &QClientSession{}
