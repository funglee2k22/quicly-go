package quicly

import (
	"fmt"
	log "github.com/rs/zerolog"
)

type Options struct {
	Logger *log.Logger

	IsClient            bool
	ApplicationProtocol string
	CongestionAlgorithm string
	CertificateFile     string
	CertificateKey      string
	IdleTimeoutMs       uint64

	TraceQuicly bool
}

func (o Options) String() string {
	return fmt.Sprintf("{%v,%v,%v,%v,%v,%v,%v}", o.IsClient, o.ApplicationProtocol, o.CongestionAlgorithm,
		o.CertificateFile, o.CertificateKey, o.IdleTimeoutMs, o.TraceQuicly)
}

func (o Options) Get() (bool, string, string, string, string, uint64, bool) {
	return o.IsClient, o.ApplicationProtocol, o.CongestionAlgorithm,
		o.CertificateFile, o.CertificateKey, o.IdleTimeoutMs, o.TraceQuicly
}
