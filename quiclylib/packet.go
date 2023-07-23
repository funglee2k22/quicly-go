package quiclylib

import (
	"hash/crc64"
	"net"
)

type packet struct {
	data    []byte
	dataLen int
	addr    *net.UDPAddr
	counter int
}

func (p *packet) Address() (string, int) {
	if p.addr == nil {
		return "", -1
	}
	return p.addr.IP.String(), p.addr.Port
}

func (p *packet) Crc64() uint64 {
	return crc64.Checksum(p.data[:p.dataLen], crc64.MakeTable(crc64.ISO))
}
