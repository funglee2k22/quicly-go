package quiclylib

import (
	"net"
	"time"
)

type QStream struct {
}

func (Q *QStream) Read(b []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) Write(b []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) Close() error {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) LocalAddr() net.Addr {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) RemoteAddr() net.Addr {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) SetDeadline(t time.Time) error {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) SetReadDeadline(t time.Time) error {
	//TODO implement me
	panic("implement me")
}

func (Q *QStream) SetWriteDeadline(t time.Time) error {
	//TODO implement me
	panic("implement me")
}

var _ net.Conn = &QStream{}
