package quiclylib

import "net"

type packet struct {
	data    []byte
	dataLen int
	addr    *net.UDPAddr
}

func (p packet) Address() (string, int) {
	return p.addr.IP.String(), p.addr.Port
}
