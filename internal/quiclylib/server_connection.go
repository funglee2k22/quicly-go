package quiclylib

import "net"

type QServerConnection struct {
}

func (Q *QServerConnection) Accept() (net.Conn, error) {
	//TODO implement me
	panic("implement me")
}

func (Q *QServerConnection) Close() error {
	//TODO implement me
	panic("implement me")
}

func (Q *QServerConnection) Addr() net.Addr {
	//TODO implement me
	panic("implement me")
}

func (Q *QServerConnection) OpenStream() Stream {
	return nil
}

var _ net.Listener = &QServerConnection{}
