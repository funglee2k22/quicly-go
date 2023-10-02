package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/Project-Faster/quic-go"
	_ "github.com/Project-Faster/quic-go"
	"github.com/Project-Faster/quicly-go"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var logger log.Logger

type qgoAdapter struct {
	quic.Stream

	locip net.Addr
	remip net.Addr
}

func (q qgoAdapter) LocalAddr() net.Addr {
	return q.locip
}

func (q qgoAdapter) RemoteAddr() net.Addr {
	return q.remip
}

func init() {
	logger = log.New(os.Stdout).With().Timestamp().Logger()
}

func main() {
	var clientFlag = flag.Bool("client", false, "Mode is client")
	var quicgoFlag = flag.Bool("quicgo", false, "Use Quicgo lib")
	var remoteHost = flag.String("host", "127.0.0.1", "Host address to use")
	var remotePort = flag.Int("port", 8443, "Port to use")
	var certFile = flag.String("cert", "server_cert.pem", "PEM certificate to use")
	var certKey = flag.String("key", "", "PEM key for the certificate")

	flag.Parse()

	//quic.

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
		if !(*quicgoFlag) {
			if len(options.CertificateFile) == 0 {
				logger.Error().Msgf("Certificate file is necessary for client execution")
				return
			}
			go runAsClient_quicly(ip, ctx)
		} else {
			go runAsClient_quic(ip, ctx)
		}

	} else {
		if !(*quicgoFlag) {
			if len(options.CertificateFile) == 0 || len(options.CertificateKey) == 0 {
				logger.Error().Msgf("Certificate file and Key necessary for server execution")
				return
			}
			go runAsServer_quicly(ip, ctx)
		} else {
			go runAsServer_quic(ip, ctx)
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	logger.Info().Msg("Received termination signal")

	quicly.Terminate()
}

func runAsClient_quic(ip *net.UDPAddr, ctx context.Context) {
	logger.Info().Msgf("Starting as client")

	options := &quic.Config{
		MaxIncomingStreams:      1024,
		DisablePathMTUDiscovery: true,

		HandshakeIdleTimeout: 10 * time.Second,
		//KeepAlivePeriod:      1 * time.Second,

		EnableDatagrams: false,
	}

	tlsConf := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"qpep"}}

	c, err := quic.DialAddr(ip.String(), tlsConf, options)
	if err != nil {
		logger.Err(err).Send()
		return
	}
	defer func() {
		c.CloseWithError(quic.ApplicationErrorCode(0), "close")
	}()

	st, err := c.OpenStream()
	if st == nil {
		logger.Err(err).Send()
		return
	}

	handleClientStream(&qgoAdapter{
		Stream: st,
		remip:  ip,
	})
}

func runAsServer_quic(ip *net.UDPAddr, ctx context.Context) {
	logger.Info().Msgf("Starting as server")

	options := &quic.Config{
		MaxIncomingStreams:      1024,
		DisablePathMTUDiscovery: true,

		HandshakeIdleTimeout: 10 * time.Second,
		//KeepAlivePeriod:      1 * time.Second,

		EnableDatagrams: false,
	}

	tlsConf := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"qpep"}}

	c, err := quic.ListenAddr(ip.String(), tlsConf, options)
	if err != nil {
		logger.Err(err).Send()
		return
	}
	defer func() {
		c.Close()
	}()

	for {
		logger.Info().Msgf("accepting...")
		conn, err := c.Accept(ctx)
		if err != nil {
			logger.Err(err).Send()
			continue
		}

		go func() {
			for {
				logger.Info().Msgf("accepting...")
				st, err := conn.AcceptStream(ctx)
				if err != nil {
					logger.Err(err).Send()
					continue
				}

				go handleServerStream(&qgoAdapter{
					Stream: st,
					locip:  ip,
					remip:  nil,
				})
			}
		}()
	}
}

func runAsClient_quicly(ip *net.UDPAddr, ctx context.Context) {
	logger.Info().Msgf("Starting as client")

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

func runAsServer_quicly(ip *net.UDPAddr, ctx context.Context) {
	logger.Info().Msgf("Starting as server")

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
		logger.Info().Msgf("Read: %n, %v\n", n, string(data[:n]))

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

		logger.Info().Msgf("SEND>> ")

		data := make([]byte, 4096)
		n, err = stream.Read(data)
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Read: %n, %v\n", n, string(data[:n]))
	}
}
