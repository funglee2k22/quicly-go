package quicly

import (
	log "github.com/rs/zerolog"
	"os"
	"time"
)

type Quicly struct {
	logger log.Logger
}

func (q *Quicly) Initialize(options *Options) {
	log.SetGlobalLevel(log.InfoLevel)
	log.TimeFieldFormat = time.StampMilli

	q.logger = log.New(os.Stdout).With().Timestamp().Logger()
	q.logger.Info().Msg("Initialized")
}

func (q *Quicly) Terminate() {
	q.logger.Info().Msg("Terminated")
}
