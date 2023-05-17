package types

import (
	"net"
)

type Session interface {
	net.Listener

	OpenStream() Stream

	OnStreamOpen(uint64)
	OnStreamClose(uint64, int)

	GetStream(uint64) Stream
}

type Stream interface {
	net.Conn

	OnReceived([]byte, int)
}
