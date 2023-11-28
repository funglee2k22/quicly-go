package quicly

import (
	"fmt"
	log "github.com/rs/zerolog"
)

type Options struct {
	Logger *log.Logger

	ApplicationProtocol string
	CongestionAlgorithm string
	CertificateFile     string
	CertificateKey      string
	IdleTimeoutMs       uint64
}

func (o Options) String() string {
	return fmt.Sprintf("{CertFile:%s,CertKey:%s}", o.CertificateFile, o.CertificateKey)
}

func (o Options) Get() (string, string, string, string, uint64) {
	return o.ApplicationProtocol, o.CongestionAlgorithm,
		o.CertificateFile, o.CertificateKey, o.IdleTimeoutMs
}
