package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/Project-Faster/quicly-go"
	"github.com/Project-Faster/quicly-go/internal/quiclylib"
	log "github.com/rs/zerolog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var logger log.Logger

func init() {
	logger = log.New(os.Stdout).With().Timestamp().Logger()
}

func main() {
	var clientFlag = flag.Bool("client", false, "Mode is client")
	var remoteHost = flag.String("host", "127.0.0.1", "Host address to use")
	var remotePort = flag.Int("port", 8443, "Port to use")

	flag.Parse()

	result := quicly.Initialize(quicly.Options{
		Logger: &logger,
		OnOpen: func() {
			logger.Print("OnStart")
		},
		OnClose: func() {
			logger.Print("OnClose")
		},
	})
	if result != quiclylib.QUICLY_OK {
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
	logger.Print("finish")
}

func runAsClient(ip *net.UDPAddr, ctx context.Context) {
	c := quicly.Dial(ip, ctx)
	defer func() {
		c.Close()
	}()

	for {
		<-time.After(100 * time.Millisecond)

		st := c.OpenStream()

		go handleClientStream(st)
		return
	}
}

func runAsServer(ip *net.UDPAddr, ctx context.Context) {
	c := quicly.Listen(ip, ctx)

	for {
		<-time.After(100 * time.Millisecond)

		st, err := c.Accept()
		if err != nil {
			logger.Err(err).Send()
			continue
		}

		go handleServerStream(st)
		return
	}
}

func handleServerStream(stream net.Conn) {
	data := make([]byte, 4096)
	for {
		n, err := stream.Read(data)
		logger.Info().Msgf("Read: %n, %v\n", n, err)
		if err != nil {
			logger.Err(err).Send()
			return
		}

		logger.Info().Msgf("received: %v\n", string(data[:n]))

		n, err = stream.Write(data[:n])
		logger.Info().Msgf("Write: %n, %v\n", n, err)
		if err != nil {
			logger.Err(err).Send()
			return
		}
	}
}

func handleClientStream(stream net.Conn) {
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		logger.Info().Msgf("Read: %d\n", len(scan.Text()))

		logger.Info().Msgf("received: %v\n", scan.Text())

		n, err := stream.Write(scan.Bytes())
		logger.Info().Msgf("Write: %n, %v\n", n, err)
		if err != nil {
			logger.Err(err).Send()
			return
		}
	}
}
