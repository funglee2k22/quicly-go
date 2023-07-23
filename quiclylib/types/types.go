package types

import (
	"net"
)

type Callbacks struct {
	OnConnectionOpen  func(connection Session)
	OnConnectionClose func(connection Session)

	OnStreamOpenCallback  func(stream Stream)
	OnStreamCloseCallback func(stream Stream, error int)
}

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

	OnOpened()
	OnReceived([]byte, int)
	OnClosed() error
}
