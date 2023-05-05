package quicly

import (
	log "github.com/rs/zerolog"
	"net"
	"os"
	"time"

	"github.com/Project-Faster/quicly-go/internal/quiclylib"
)

var logger log.Logger
var opt Options

func Initialize(options Options) {
	log.SetGlobalLevel(log.InfoLevel)
	log.TimeFieldFormat = time.StampMilli

	if options.Logger == nil {
		logger = log.New(os.Stdout).With().Timestamp().Logger()
	} else {
		logger = *options.Logger
	}

	opt = options

	quiclylib.InitializeQuiclyEngine()

	logger.Info().Msg("Initialized")

	if opt.OnOpen != nil {
		opt.OnOpen()
	}
}

func Terminate() {
	quiclylib.CloseQuiclyEngine()
	logger.Info().Msg("Terminated")
	if opt.OnClose != nil {
		opt.OnClose()
	}
}

func Listen(localAddr *net.UDPAddr) quiclylib.Session {
	udpConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		logger.Error().Msgf("Could not listen on specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QServerSession{
		Conn: udpConn,
	}

	return conn
}

func Dial(remoteAddr *net.UDPAddr) quiclylib.Session {
	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		logger.Error().Msgf("Could not dial the specified udp address: %v", err)
		return nil
	}

	conn := &quiclylib.QClientSession{
		Conn: udpConn,
	}

	return conn
}
