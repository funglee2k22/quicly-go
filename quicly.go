package quicly

import (
	log "github.com/rs/zerolog"
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
