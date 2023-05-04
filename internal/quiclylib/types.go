package quiclylib

import (
	"net"
)

type Connection interface {
	net.Listener

	OpenStream() Stream
}

var _ Connection = &QServerConnection{}

type Stream interface {
	net.Conn
}

var _ Stream = &QStream{}
