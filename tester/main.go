package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/Project-Faster/quic-go"
	"github.com/Project-Faster/quicly-go"
	"github.com/Project-Faster/quicly-go/quiclylib"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
)

var logger log.Logger
var executorWaitGroup sync.WaitGroup
var payloadRef = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789=")

type qgoAdapter struct {
	quic.Stream

	locip net.Addr
	remip net.Addr
}

type TesterOptions struct {
	quicly.Options
}

type testerFunc func(wgOut *sync.WaitGroup, ip *net.UDPAddr, ctx context.Context, cancel context.CancelFunc)

func (q qgoAdapter) LocalAddr() net.Addr {
	return q.locip
}

func (q qgoAdapter) RemoteAddr() net.Addr {
	return q.remip
}

var startPrefix = 0
var logFile *os.File = nil

func removePrefixCaller(pc uintptr, file string, line int) string {
	if startPrefix == 0 {
		startPrefix = strings.Index(file, "quicly-go/")
		startPrefix += len("quicly-go/")
	}
	return fmt.Sprintf("%s:%d", file[startPrefix:], line)
}

func init() {
	log.CallerMarshalFunc = removePrefixCaller

	logger = log.New(os.Stdout).Level(log.DebugLevel).
		With().Timestamp().
		Caller().
		Logger()
}

var verboseFlag = flag.Bool("verbose", false, "Verbose output")
var traceFlag = flag.Bool("trace", false, "Trace output")
var clientFlag = flag.Bool("client", false, "Operate as client")
var keepOpenFlag = flag.Bool("keep", false, "Do not terminate after first stream")
var quicgoFlag = flag.Bool("quicgo", false, "Use QuicGo lib")
var remoteHost = flag.String("host", "127.0.0.1", "Host address to use")
var remotePort = flag.Int("port", 8443, "Port to use")
var certFile = flag.String("cert", "server_cert.pem", "PEM certificate to use")
var certKey = flag.String("key", "", "PEM key for the certificate")
var randServerPayload = flag.Int("payload", 4096, "Random payload to download on server connection")
var ccaFlag = flag.String("cca", "reno", "Congestion algorithm to use (reno|cubic|pico)")
var ccaSlowFlag = flag.String("ccslow", "basic", "Slowstart algorithm to use (basic|search)")

func main() {
	flag.Parse()

	if *clientFlag {
		logFile, _ = os.Create("tester-client.log")
	} else {
		logFile, _ = os.Create("tester-server.log")
	}
	defer logFile.Close()

	log.TimeFieldFormat = time.StampMilli

	var lvl = log.InfoLevel
	if *verboseFlag {
		lvl = log.DebugLevel
	}
	logger = log.New(io.MultiWriter(os.Stdout, logFile)).Level(lvl).
		With().Timestamp().
		Caller().
		Logger()

	options := quicly.Options{
		Logger:               &logger,
		IsClient:             *clientFlag,
		CertificateFile:      *certFile,
		CertificateKey:       *certKey,
		ApplicationProtocol:  "qpep_quicly",
		IdleTimeoutMs:        60000,
		CongestionAlgorithm:  *ccaFlag,
		CCSlowstartAlgorithm: *ccaSlowFlag,
		TraceQuicly:          *traceFlag,
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

	str := &bytes.Buffer{}
	for i := 0; i < *randServerPayload; i++ {
		str.WriteByte(payloadRef[rand.Intn(len(payloadRef))])
	}
	ctx = context.WithValue(ctx, "Payload", str)

	var tester_runner testerFunc

	if *clientFlag {
		if len(options.CertificateFile) == 0 {
			logger.Error().Msgf("Certificate file is necessary for client execution")
			return
		}
		if !(*quicgoFlag) {
			tester_runner = runAsClient_quicly
		} else {
			tester_runner = runAsClient_quicgo
		}

	} else {
		if len(options.CertificateFile) == 0 || len(options.CertificateKey) == 0 {
			logger.Error().Msgf("Certificate file and Key necessary for server execution")
			return
		}
		if !(*quicgoFlag) {
			tester_runner = runAsServer_quicly
		} else {
			tester_runner = runAsServer_quicgo
		}
	}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		logger.Warn().Msg("Received termination signal")

		cancel()
	}()

	executorWaitGroup.Add(1)
	tester_runner(&executorWaitGroup, ip, ctx, cancel)

	if !(*quicgoFlag) {
		logger.Warn().Msg("term...")
		quicly.Terminate()
		logger.Warn().Msg("terminated")
	}

	logger.Warn().Msg("Closing...")

	executorWaitGroup.Wait()

	logger.Warn().Msg("Closed")
	os.Exit(0)
}

func runAsClient_quicgo(wgOut *sync.WaitGroup, ip *net.UDPAddr, ctx context.Context, cancel context.CancelFunc) {
	logger.Info().Msgf("Starting as client")
	defer wgOut.Done()

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
		cancel()
	}()

	st, err := c.OpenStream()
	if st == nil {
		logger.Err(err).Send()
		return
	}

	defer logger.Info().Msgf("END: runAsClient_quicgo")

	wg := &sync.WaitGroup{}
	wg.Add(2)

	ctxSt, cancelSt := context.WithCancel(context.Background())

	go handleClientStreamRead(wg, ctxSt, cancelSt, &qgoAdapter{
		Stream: st,
		remip:  ip,
	})
	go handleClientStreamWrite(wg, ctxSt, cancelSt, &qgoAdapter{
		Stream: st,
		remip:  ip,
	})

	wg.Wait()
}

func runAsServer_quicgo(wgOut *sync.WaitGroup, ip *net.UDPAddr, ctx context.Context, cancel context.CancelFunc) {
	logger.Info().Msgf("Starting as server")
	defer wgOut.Done()

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
		cancel()
	}()

	defer logger.Info().Msgf("END: runAsServer_quicgo")

	fulldata := ctx.Value("Payload").(*bytes.Buffer)
	dumpDataToFile("server", fulldata)
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
					logger.Error().Msgf("error accepting stream: %v", err)
					return
				}

				go func() {
					logger.Info().Msgf("accepted stream id: %v", st.StreamID())

					wg := &sync.WaitGroup{}
					wg.Add(2)

					ctxSt, cancelSt := context.WithCancel(context.Background())

					go handleServerStreamWrite(wg, ctxSt, cancelSt, &qgoAdapter{
						Stream: st,
						locip:  ip,
						remip:  nil,
					}, bytes.NewBuffer(fulldata.Bytes()))

					go handleServerStreamRead(wg, ctxSt, cancelSt, &qgoAdapter{
						Stream: st,
						locip:  ip,
						remip:  nil,
					})

					wg.Wait()
					logger.Info().Msgf("terminated stream id: %v", st.StreamID())

					if !*keepOpenFlag {
						cancel()
					}
				}()
			}
		}()
	}
}

func runAsClient_quicly(wgOut *sync.WaitGroup, ip *net.UDPAddr, ctx context.Context, cancel context.CancelFunc) {
	logger.Info().Msgf("Starting as client")
	defer wgOut.Done()

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
		cancel()
	}()

	st := c.OpenStream()
	if st == nil {
		return
	}

	defer func() {
		_ = st.Close()
		logger.Info().Msgf("END: runAsClient_quicly")
	}()

	wg := &sync.WaitGroup{}
	wg.Add(2)

	ctxSt, cancelSt := context.WithCancel(context.Background())

	go handleClientStreamRead(wg, ctxSt, cancelSt, st)
	go handleClientStreamWrite(wg, ctxSt, cancelSt, st)

	wg.Wait()
}

func runAsServer_quicly(wgOut *sync.WaitGroup, ip *net.UDPAddr, ctx context.Context, cancel context.CancelFunc) {
	logger.Info().Msgf("Starting as server")
	defer wgOut.Done()

	serverListener := quicly.Listen(ip, types.Callbacks{
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
		logger.Info().Msgf("END: runAsServer_quicly")
		cancel()
	}()

	fulldata := ctx.Value("Payload").(*bytes.Buffer)
	dumpDataToFile("server", fulldata)
	for {
		logger.Info().Msgf("accepting connection...")
		conn, err := serverListener.Accept()
		if err != nil {
			logger.Err(err).Send()
			return
		}

		go func() {
			defer logger.Info().Msgf("closed connection from: %v", conn.Addr())
			logger.Info().Msgf("accepting stream...")
			st, err := conn.Accept()
			if err != nil {
				logger.Err(err).Send()
				return
			}

			go func() {
				logger.Info().Msgf("accepted stream from %v", st)
				defer func() {
					_ = st.Close()
					logger.Info().Msgf("END: runAsServer_quicly connection")
				}()

				wg := &sync.WaitGroup{}
				wg.Add(2)

				ctxSt, cancelSt := context.WithCancel(context.Background())

				go handleServerStreamRead(wg, ctxSt, cancelSt, st)
				go handleServerStreamWrite(wg, ctxSt, cancelSt, st, bytes.NewBuffer(fulldata.Bytes()))

				wg.Wait()

				if !*keepOpenFlag {
					cancel()
				}
			}()
		}()
	}
}

func handleServerStreamRead(wg *sync.WaitGroup, ctx context.Context, cancel context.CancelFunc, stream net.Conn) {
	total := 0
	startTime := time.Now()
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
		wg.Done()
		cancel()
		logger.Info().Msgf("END: handleServerStreamRead (%d)(%v)", total, time.Since(startTime))
	}()

	data := make([]byte, quiclylib.READ_SIZE)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			break
		}

		_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := stream.Read(data)
		total += n
		if n > 0 {
			logger.Info().Msgf("Read(%d)", n)
			logger.Debug().Msgf("Data(%d): %v", n, string(data[:n]))
		}
		if err == nil {
			continue
		}
		var netErr, ok = err.(net.Error)
		if (!ok && err != io.EOF) || (ok && !netErr.Timeout()) {
			logger.Err(err).Send()
			return
		}
	}
}

func handleServerStreamWrite(wg *sync.WaitGroup, ctx context.Context, cancel context.CancelFunc, stream net.Conn, buffer *bytes.Buffer) {
	total := 0
	startTime := time.Now()
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
		wg.Done()
		cancel()
		logger.Info().Msgf("END: handleServerStreamWrite (%d)(%v)", total, time.Since(startTime))
	}()

	data := make([]byte, quiclylib.READ_SIZE)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			break
		}

		n, err := buffer.Read(data)
		if err != nil {
			logger.Err(err).Send()
			return
		}

		_ = stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		n, err = stream.Write(data[:n])
		total += n
		if n > 0 {
			logger.Info().Msgf("Write(%d)", n)
			logger.Debug().Msgf("Data(%d): %v", n, string(data[:n]))
		}
		if err == nil {
			continue
		}
		var netErr, ok = err.(net.Error)
		if (!ok && err != io.EOF) || (ok && !netErr.Timeout()) {
			logger.Err(err).Send()
			return
		}
	}
}

func dumpDataToFile(prefix string, buf *bytes.Buffer) {
	if false {
		name := fmt.Sprintf("%s_%v.bin", prefix, time.Now().Unix())
		_ = os.WriteFile(name, buf.Bytes(), 0777)

		logger.Info().Msgf("RECV DATA dumped to: %s (len: %d)", name, buf.Len())
	}
}

func handleClientStreamRead(wg *sync.WaitGroup, ctx context.Context, cancel context.CancelFunc, stream net.Conn) {
	buf := bytes.NewBuffer(make([]byte, 0, quiclylib.READ_SIZE))
	total := 0
	startTime := time.Now()
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
		wg.Done()
		cancel()
		logger.Info().Msgf("END: handleClientStreamRead (%d)(%v)", total, time.Since(startTime))
	}()
	defer dumpDataToFile("client", buf)

	data := make([]byte, quiclylib.READ_SIZE)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			break
		}

		_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := stream.Read(data)
		total += n
		if n > 0 {
			logger.Info().Msgf("Read(%d)", n)
			logger.Debug().Msgf("Data(%d): %v", n, string(data[:n]))
		}
		if err == nil {
			continue
		}
		var netErr, ok = err.(net.Error)
		if (!ok && err != io.EOF) || (ok && !netErr.Timeout()) {
			logger.Err(err).Send()
			return
		}
	}
}

func handleClientStreamWrite(wg *sync.WaitGroup, ctx context.Context, cancel context.CancelFunc, stream net.Conn) {
	total := 0
	startTime := time.Now()
	defer func() {
		if err := recover(); err != nil {
			logger.Error().Msgf("err: %v", err)
			debug.PrintStack()
		}
		wg.Done()
		cancel()
		logger.Info().Msgf("END: handleClientStreamWrite (%d)(%v)", total, time.Since(startTime))
	}()

	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			break
		}

		_ = stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		data := scan.Bytes()
		n, err := stream.Write(data)
		total += n
		if n > 0 {
			logger.Info().Msgf("Write(%d)", n)
			logger.Debug().Msgf("Data(%d): %v", n, string(data[:n]))
		}
		if err == nil {
			continue
		}
		var netErr, ok = err.(net.Error)
		if (!ok && err != io.EOF) || (ok && !netErr.Timeout()) {
			logger.Err(err).Send()
			return
		}
	}
}
