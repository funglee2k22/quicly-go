package quiclylib

import "net"

type QClientConnection struct {
}

func (Q *QClientConnection) Accept() (net.Conn, error) {
	//TODO implement me
	panic("implement me")
}

func (Q *QClientConnection) Close() error {
	//TODO implement me
	panic("implement me")
}

func (Q *QClientConnection) Addr() net.Addr {
	//TODO implement me
	panic("implement me")
}

func (Q *QClientConnection) OpenStream() Stream {
	return nil
}

var _ net.Listener = &QClientConnection{}
