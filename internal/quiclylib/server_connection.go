package quiclylib

import "net"

type QServerSession struct {
	id uint32

	Conn *net.UDPConn
}

func (s *QServerSession) ID() uint32 {
	return s.id
}

func (s *QServerSession) Accept() (net.Conn, error) {
	st := &QStream{
		session: s,
		conn:    s.Conn,
	}
	st.init()
	return st, nil
}

func (s *QServerSession) Close() error {
	if s.Conn == nil {
		return nil
	}
	defer func() {
		s.Conn = nil
	}()
	return s.Conn.Close()
}

func (s *QServerSession) Addr() net.Addr {
	if s.Conn == nil {
		return nil
	}
	return s.Conn.LocalAddr()
}

func (s *QServerSession) OpenStream() Stream {
	return nil
}

var _ net.Listener = &QServerSession{}
