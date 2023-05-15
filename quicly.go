package quicly

import (
	"github.com/Project-Faster/quicly-go/quiclylib"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"

	"context"
	log "github.com/rs/zerolog"
	"net"
	"os"
	"time"
)

var logger log.Logger
var opt Options

func Initialize(options Options) int {
	log.SetGlobalLevel(log.InfoLevel)
	log.TimeFieldFormat = time.StampMilli

	if options.Logger == nil {
		logger = log.New(os.Stdout).With().Timestamp().Logger()
	} else {
		logger = *options.Logger
	}

	opt = options

	result := quiclylib.QuiclyInitializeEngine()
	if result != errors.QUICLY_OK {
		logger.Error().Msgf("Failed initialization: %v", result)
		return result
	}

	logger.Info().Msg("Initialized")

	if opt.OnOpen != nil {
		opt.OnOpen()
	}
	return errors.QUICLY_OK
}

func Terminate() {
	quiclylib.QuiclyCloseEngine()
	logger.Info().Msg("Terminated")
	if opt.OnClose != nil {
		opt.OnClose()
	}
}

func Listen(localAddr *net.UDPAddr, ctx context.Context) quiclylib.Session {
	udpConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		logger.Error().Msgf("Could not listen on specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QServerSession{
		Conn: udpConn,
		Ctx:  ctx,
	}

	return conn
}

func Dial(remoteAddr *net.UDPAddr, ctx context.Context) quiclylib.Session {
	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		logger.Error().Msgf("Could not dial the specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QClientSession{
		Conn: udpConn,
		Ctx:  ctx,
	}

	return conn
}
