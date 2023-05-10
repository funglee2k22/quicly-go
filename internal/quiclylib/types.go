package quiclylib

import (
	"net"
)

type Session interface {
	net.Listener

	OpenStream() Stream
}

var _ Session = &QServerSession{}

type Stream interface {
	net.Conn
}

var _ Stream = &QStream{}
