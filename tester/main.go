package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/Project-Faster/quicly-go"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
)

var logger log.Logger

func init() {
	logger = log.New(os.Stdout).With().Timestamp().Logger()
}

func main() {
	var clientFlag = flag.Bool("client", false, "Mode is client")
	var remoteHost = flag.String("host", "127.0.0.1", "Host address to use")
	var remotePort = flag.Int("port", 8443, "Port to use")
	var certFile = flag.String("cert", "server_cert.pem", "PEM certificate to use")
	var certKey = flag.String("key", "", "PEM key for the certificate")

	flag.Parse()

	options := quicly.Options{
		Logger:          &logger,
		CertificateFile: *certFile,
		CertificateKey:  *certKey,
	}

	logger.Info().Msgf("Options: %v", options)

	result := quicly.Initialize(options)
	if result != errors.QUICLY_OK {
		logger.Error().Msgf("Failed initialization: %v", result)
		os.Exit(1)
	}

	ip, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", *remoteHost, *remotePort))
	if err != nil {
		logger.Err(err).Send()
		return
	}

	ctx := context.Background()

	if *clientFlag {
		go runAsClient(ip, ctx)
	} else {
		go runAsServer(ip, ctx)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	logger.Info().Msg("Received termination signal")

	quicly.Terminate()
}

func runAsClient(ip *net.UDPAddr, ctx context.Context) {
	c := quicly.Dial(ip, types.Callbacks{
		OnConnectionOpen: func(conn types.Session) {
			logger.Print("OnStart")
		},
		OnConnectionClose: func(conn types.Session) {
			logger.Print("OnClose")
		},
		OnStreamOpenCallback: func(stream types.Stream) {
			logger.Info().Msgf(">> Callback open %d", stream.ID())
		},
		OnStreamCloseCallback: func(stream types.Stream, error int) {
			logger.Info().Msgf(">> Callback close %d, error %d", stream.ID(), error)
		},
	}, ctx)
	defer func() {
		c.Close()
	}()

	st := c.OpenStream()
	if st == nil {
		return
	}

	handleClientStream(st)
}

func runAsServer(ip *net.UDPAddr, ctx context.Context) {
	c := quicly.Listen(ip, types.Callbacks{
		OnConnectionOpen: func(conn types.Session) {
			logger.Print("OnStart")
		},
		OnConnectionClose: func(conn types.Session) {
			logger.Print("OnClose")
		},
		OnStreamOpenCallback: func(stream types.Stream) {
			logger.Info().Msgf(">> Callback open %d", stream.ID())
		},
		OnStreamCloseCallback: func(stream types.Stream, error int) {
			logger.Info().Msgf(">> Callback close %d, error %d", stream.ID(), error)
		},
	}, ctx)

	for {
		logger.Info().Msgf("accepting...")
		st, err := c.Accept()
		if err != nil {
			logger.Err(err).Send()
			continue
		}

		go handleServerStream(st)
	}
}

func handleServerStream(stream net.Conn) {
	data := make([]byte, 4096)
	for {
		n, err := stream.Read(data)
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Read: %n, %v\n", n, err, string(data[:n]))

		n, err = stream.Write(data[:n])
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Write: %n, %v\n", n, err)
	}
}

func handleClientStream(stream net.Conn) {
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		n, err := stream.Write(scan.Bytes())
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Write: %n, %v\n", n, err)

		data := make([]byte, 4096)
		n, err = stream.Read(data)
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Read: %n, %v\n", n, err, string(data[:n]))
	}
}
