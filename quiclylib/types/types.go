package types

import (
	"net"
)

type Session interface {
	net.Listener

	ID() uint64

	OpenStream() Stream

	OnStreamOpen(uint64)
	OnStreamClose(uint64, int)

	GetStream(uint64) Stream
}

type Stream interface {
	net.Conn

	ID() uint64

	OnReceived([]byte, int)
}
