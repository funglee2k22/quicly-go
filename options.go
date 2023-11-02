package quicly

import (
	"fmt"
	log "github.com/rs/zerolog"
)

type Options struct {
	Logger *log.Logger

	ApplicationProtocol string
	CertificateFile     string
	CertificateKey      string
	IdleTimeoutMs       uint64
}

func (o Options) String() string {
	return fmt.Sprintf("{CertFile:%s,CertKey:%s}", o.CertificateFile, o.CertificateKey)
}
