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
	if options.Logger == nil {
		log.SetGlobalLevel(log.InfoLevel)
		log.TimeFieldFormat = time.StampMilli
		logger = log.New(os.Stdout).With().Timestamp().Logger()
	} else {
		logger = *options.Logger
	}

	result := quiclylib.QuiclyInitializeEngine(options)
	if result != errors.QUICLY_OK {
		logger.Error().Msgf("Failed initialization: %v", result)
		return result
	}

	logger.Debug().Msg("Initialized")
	return errors.QUICLY_OK
}

func Terminate() {
	quiclylib.QuiclyCloseEngine()
	logger.Debug().Msg("Terminated")
}

func Listen(localAddr *net.UDPAddr, cb types.Callbacks, ctx context.Context) types.ServerSession {
	udpConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		logger.Error().Msgf("Could not listen on specified udp address: %v", err)
		return nil
	}

	_ = udpConn.SetReadBuffer(32 * 1280 * 1024)
	_ = udpConn.SetWriteBuffer(32 * 1280 * 1024)

	conn := &quiclylib.QServerSession{
		NetConn:   udpConn,
		Ctx:       ctx,
		Logger:    logger.With().Timestamp().Logger(),
		Callbacks: cb,
	}

	return conn
}

func Dial(remoteAddr *net.UDPAddr, cb types.Callbacks, ctx context.Context) types.ClientSession {
	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		logger.Error().Msgf("Could not dial the specified udp address: %v", err)
		return nil
	}

	_ = udpConn.SetReadBuffer(32 * 1280 * 1024)
	_ = udpConn.SetWriteBuffer(32 * 1280 * 1024)
	
	conn := &quiclylib.QClientSession{
		NetConn:   udpConn,
		Ctx:       ctx,
		Logger:    logger.With().Timestamp().Logger(),
		Callbacks: cb,
	}

	conn.Logger.Debug().Msgf("[%v] CONNECT %p", conn.ID(), conn)

	return conn
}
