package types

import (
	"net"
)

type Session interface {
	net.Listener

	OpenStream() Stream

	OnStreamOpen(uint64)
	OnStreamClose(uint64, int)
}

type Stream interface {
	net.Conn
}
