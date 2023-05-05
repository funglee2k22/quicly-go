package quiclylib

import (
	"net"
)

type Session interface {
	net.Listener

	OpenStream() Stream

	ID() uint32
}

var _ Session = &QServerSession{}

type Stream interface {
	net.Conn

	ID() uint32
}

var _ Stream = &QStream{}
