package quicly

import (
	log "github.com/rs/zerolog"
	"net"
	"os"
	"time"

	"github.com/Project-Faster/quicly-go/internal/quiclylib"
)

type Quicly struct {
	logger log.Logger
}

func (q *Quicly) Initialize(options Options) {
	log.SetGlobalLevel(log.InfoLevel)
	log.TimeFieldFormat = time.StampMilli

	if options.Logger == nil {
		q.logger = log.New(os.Stdout).With().Timestamp().Logger()
	} else {
		q.logger = *options.Logger
	}

	quiclylib.InitializeQuiclyEngine()

	q.logger.Info().Msg("Initialized")
}

func (q *Quicly) Terminate() {
	quiclylib.CloseQuiclyEngine()
	q.logger.Info().Msg("Terminated")
}

func (q *Quicly) Listen(localAddr net.Addr) quiclylib.Connection {
	conn := &quiclylib.QServerConnection{}

	return conn
}

func (q *Quicly) Dial(remoteAddr net.Addr) quiclylib.Connection {
	conn := &quiclylib.QClientConnection{}

	return conn
}
