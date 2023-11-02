package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/Project-Faster/quic-go"
	"github.com/Project-Faster/quicly-go"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

var logger log.Logger
var executorWaitGroup sync.WaitGroup

type qgoAdapter struct {
	quic.Stream

	locip net.Addr
	remip net.Addr
}

type TesterOptions struct {
	quicly.Options
}

type tester_func func(ip *net.UDPAddr, ctx context.Context)

func (q qgoAdapter) LocalAddr() net.Addr {
	return q.locip
}

func (q qgoAdapter) RemoteAddr() net.Addr {
	return q.remip
}

func init() {
	logger = log.New(os.Stdout).With().Timestamp().Caller().Logger()
}

func main() {
	var clientFlag = flag.Bool("client", false, "Operate as client")
	var quicgoFlag = flag.Bool("quicgo", false, "Use QuicGo lib")
	var remoteHost = flag.String("host", "127.0.0.1", "Host address to use")
	var remotePort = flag.Int("port", 8443, "Port to use")
	var certFile = flag.String("cert", "server_cert.pem", "PEM certificate to use")
	var certKey = flag.String("key", "", "PEM key for the certificate")

	flag.Parse()

	options := quicly.Options{
		Logger:              &logger,
		CertificateFile:     *certFile,
		CertificateKey:      *certKey,
		ApplicationProtocol: "qpep_quicly",
		IdleTimeoutMs:       3000,
	}

	logger.Info().Msgf("Options: %v", options)

	if !(*quicgoFlag) {
		result := quicly.Initialize(options)
		if result != errors.QUICLY_OK {
			logger.Error().Msgf("Failed initialization: %v", result)
			os.Exit(1)
		}
	}

	ip, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", *remoteHost, *remotePort))
	if err != nil {
		logger.Err(err).Send()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "Cert", options.CertificateFile)
	ctx = context.WithValue(ctx, "Key", options.CertificateKey)

	var tester_runner tester_func

	if *clientFlag {
		if len(options.CertificateFile) == 0 {
			logger.Error().Msgf("Certificate file is necessary for client execution")
			return
		}
		if !(*quicgoFlag) {
			tester_runner = runAsClient_quicly
		} else {
			tester_runner = runAsClient_quic
		}

	} else {
		if len(options.CertificateFile) == 0 || len(options.CertificateKey) == 0 {
			logger.Error().Msgf("Certificate file and Key necessary for server execution")
			return
		}
		if !(*quicgoFlag) {
			tester_runner = runAsServer_quicly
		} else {
			tester_runner = runAsServer_quic
		}
	}

	go tester_runner(ip, ctx)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	logger.Warn().Msg("Received termination signal")

	cancel()
	executorWaitGroup.Wait()

	logger.Warn().Msg("Termination done")

	if !(*quicgoFlag) {
		quicly.Terminate()
	}
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
		_ = c.CloseWithError(quic.ApplicationErrorCode(0), "close")
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

	certFile := ctx.Value("Cert").(string)
	keyFile := ctx.Value("Key").(string)

	certPEM, err := ioutil.ReadFile(certFile)
	if err != nil {
		panic(err)
	}
	keyPEM, err := ioutil.ReadFile(keyFile)
	if err != nil {
		panic(err)
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"qpep"},
	}

	c, err := quic.ListenAddr(ip.String(), tlsConf, options)
	if err != nil {
		logger.Err(err).Send()
		return
	}
	defer func() {
		c.Close()
	}()

	for {
		logger.Info().Msgf("accepting connection...")
		conn, err := c.Accept(ctx)
		if err != nil {
			logger.Err(err).Send()
			<-time.After(100 * time.Millisecond)
			return
		}

		logger.Info().Msgf("accepted connection from: %v", conn.RemoteAddr())
		go func() {
			defer logger.Info().Msgf("closed connection from: %v", conn.RemoteAddr())
			for {
				logger.Info().Msgf("accepting stream...")
				st, err := conn.AcceptStream(ctx)
				if err != nil {
					return
				}

				logger.Info().Msgf("accepted stream from: %v", st.StreamID())
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
			return
		}

		go handleServerStream(st)
	}
}

func handleServerStream(stream net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
	}()

	data := make([]byte, 4096)
	for {
		logger.Info().Msgf("Read Stream")

		_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := stream.Read(data)
		logger.Info().Msgf("err: %v", err)
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Read(%d): %v\n", n, string(data[:n]))

		logger.Info().Msgf("Write Stream")

		_ = stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		n, err = stream.Write(data[:n])
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Write(%d): %v\n", n, string(data[:n]))
	}
}

func handleClientStream(stream net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
	}()

	data := make([]byte, 4096)

	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		logger.Info().Msgf("Write Stream")

		_ = stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := stream.Write(scan.Bytes())
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Write(%d): %v\n", n, scan.Text())

		logger.Info().Msgf("Read Stream")

		_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err = stream.Read(data)
		if err != nil {
			if err != io.EOF {
				logger.Err(err).Send()
				return
			}
			continue
		}
		logger.Info().Msgf("Read(%d): %v\n", n, string(data[:n]))

		logger.Info().Msgf("SEND>> ")
	}
}
