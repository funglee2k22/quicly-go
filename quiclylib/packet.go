package quiclylib

import (
	"hash/crc64"
	"net"
)

type packet struct {
	streamid uint64
	data     []byte
	dataLen  int
	addr     *net.UDPAddr
}

func (p *packet) Address() (string, int) {
	if p == nil || p.addr == nil {
		return "", -1
	}
	return p.addr.IP.String(), p.addr.Port
}

func (p *packet) Crc64() uint64 {
	return crc64.Checksum(p.data[:p.dataLen], crc64.MakeTable(crc64.ISO))
}
