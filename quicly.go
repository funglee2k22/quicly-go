package quicly

import (
	"github.com/Project-Faster/quicly-go/quiclylib"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"

	"context"
	log "github.com/rs/zerolog"
	"net"
	"os"
	"time"
)

var logger log.Logger

func Initialize(options Options) int {
	log.SetGlobalLevel(log.InfoLevel)
	log.TimeFieldFormat = time.StampMilli

	if options.Logger == nil {
		logger = log.New(os.Stdout).With().Timestamp().Logger()
	} else {
		logger = *options.Logger
	}

	result := quiclylib.QuiclyInitializeEngine(
		options.ApplicationProtocol,
		options.CertificateFile, options.CertificateKey,
		options.IdleTimeoutMs)
	if result != errors.QUICLY_OK {
		logger.Error().Msgf("Failed initialization: %v", result)
		return result
	}

	logger.Info().Msg("Initialized")
	return errors.QUICLY_OK
}

func Terminate() {
	quiclylib.QuiclyCloseEngine()
	logger.Info().Msg("Terminated")
}

func Listen(localAddr *net.UDPAddr, cb types.Callbacks, ctx context.Context) types.Session {
	udpConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		logger.Error().Msgf("Could not listen on specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QServerSession{
		Conn:      udpConn,
		Ctx:       ctx,
		Logger:    logger.With().Timestamp().Logger(),
		Callbacks: cb,
	}

	return conn
}

func Dial(remoteAddr *net.UDPAddr, cb types.Callbacks, ctx context.Context) types.Session {
	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		logger.Error().Msgf("Could not dial the specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QClientSession{
		Conn:      udpConn,
		Ctx:       ctx,
		Logger:    logger.With().Timestamp().Logger(),
		Callbacks: cb,
	}

	return conn
}
