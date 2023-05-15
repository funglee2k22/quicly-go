package quiclylib

import (
	"net"
	"time"
)

type QStream struct {
	session Session
	conn    net.Conn
}

func (s *QStream) Read(b []byte) (n int, err error) {
	if s.conn == nil {
		return -1, net.ErrClosed
	}
	//n, s.returnAddr, err = s.conn.(*net.UDPConn).ReadFromUDP(b)
	return n, err
}

func (s *QStream) Write(b []byte) (n int, err error) {
	if s.conn == nil {
		return -1, net.ErrClosed
	}
	//return s.conn.(*net.UDPConn).WriteToUDP(b, s.returnAddr)
	return n, err
}

func (s *QStream) Close() error {
	return nil
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
	if s.conn != nil {
		return nil
	}
	return s.conn.SetDeadline(t)
}

func (s *QStream) SetReadDeadline(t time.Time) error {
	if s.conn != nil {
		return nil
	}
	return s.conn.SetReadDeadline(t)
}

func (s *QStream) SetWriteDeadline(t time.Time) error {
	if s.conn != nil {
		return nil
	}
	return s.conn.SetWriteDeadline(t)
}

var _ net.Conn = &QStream{}
