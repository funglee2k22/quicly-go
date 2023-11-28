package types

import (
	"hash/crc64"
	"net"
)

type Callbacks struct {
	OnConnectionOpen  func(connection Session)
	OnConnectionClose func(connection Session)

	OnStreamOpenCallback  func(stream Stream)
	OnStreamCloseCallback func(stream Stream, error int)
}

type Options interface {
	Get() (string, string, string, string, uint64)
}

type Session interface {
	ID() uint64

	OnStreamOpen(uint64)
	OnStreamClose(uint64, int)

	GetStream(uint64) Stream

	StreamPacket(*Packet)

	// Addr returns the listener's network address.
	Addr() net.Addr

	// Close closes the listener.
	// Any blocked Accept operations will be unblocked and return errors.
	Close() error
}

type ServerSession interface {
	// Accept waits for and returns the next connection to the listener.
	Accept() (ServerConnection, error)
}

type ServerConnection interface {
	// Accept waits for and returns the next connection to the listener.
	Accept() (Stream, error)

	Session
}

type ClientSession interface {
	OpenStream() Stream

	Session
}

type Stream interface {
	net.Conn

	ID() uint64

	OnOpened()
	OnReceived([]byte, int)
	OnClosed() error
}

type Packet struct {
	Streamid   uint64
	Data       []byte
	DataLen    int
	RetAddress *net.UDPAddr
}

func (p *Packet) Address() (string, int) {
	if p == nil || p.RetAddress == nil {
		return "", -1
	}
	return p.RetAddress.IP.String(), p.RetAddress.Port
}

func (p *Packet) Crc64() uint64 {
	return crc64.Checksum(p.Data[:p.DataLen], crc64.MakeTable(crc64.ISO))
}
