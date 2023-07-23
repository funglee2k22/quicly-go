package quiclylib

import "C"
import (
	"context"
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/errors"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	log "github.com/rs/zerolog"
	"net"
	"sync"
	"time"
)

type QClientSession struct {
	// exported fields
	Conn   *net.UDPConn
	Ctx    context.Context
	Logger log.Logger

	// callback
	types.Callbacks

	// unexported fields
	id        uint64
	connected bool

	streams     map[uint64]types.Stream
	streamsLock sync.RWMutex

	incomingQueue []packet
	inLock        sync.RWMutex

	outgoingQueue []packet
	outLock       sync.RWMutex

	dataQueue []packet
	dataLock  sync.RWMutex
}

func (s *QClientSession) connectionInHandler() {
	var buffList = make([][]byte, 0, 512)
	for i := 0; i < 512; i++ {
		buffList = append(buffList, make([]byte, BUF_SIZE))
	}

	s.incomingQueue = make([]packet, 0, 512)
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		n, addr, err := s.Conn.ReadFromUDP(buffList[0])
		if n == 0 || (n == 0 && err != nil) {
			<-time.After(100 * time.Microsecond)
			continue
		}

		buf := buffList[0]
		buffList = buffList[1:]
		if len(buffList) == 0 {
			for i := 0; i < 512; i++ {
				buffList = append(buffList, make([]byte, BUF_SIZE))
			}
		}

		s.Logger.Debug().Msgf("QUICLY IN packet (%d) from %v", n, addr.String())

		s.inLock.Lock()
		s.incomingQueue = append(s.incomingQueue, packet{
			data:    buf[:n],
			dataLen: n,
			addr:    addr,
		})
		s.inLock.Unlock()
	}
}

func (s *QClientSession) connectionProcessHandler() {
	s.outgoingQueue = make([]packet, 0, 512)

	var num_packets = bindings.Size_t(128)
	var packets_buf = make([]bindings.Iovec, 128)

	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(1 * time.Millisecond):
		default:
		}

		var returnAddr *net.UDPAddr = nil

		s.inLock.RLock()
		emptyCheck := len(s.incomingQueue) == 0
		s.inLock.RUnlock()

		if !emptyCheck {
			s.inLock.Lock()
			pkt := s.incomingQueue[0]
			s.incomingQueue = s.incomingQueue[1:]
			s.inLock.Unlock()

			returnAddr = pkt.addr
			addr, port := pkt.Address()
			if len(addr) == 0 || port == -1 {
				s.Logger.Debug().Msgf("QUICLY Received %d bytes (dropped)", pkt.dataLen)
				continue
			}

			var ptr_id bindings.Size_t = 0

			s.Logger.Debug().Msgf("QUICLY Received %d bytes", pkt.dataLen)

			err := bindings.QuiclyProcessMsg(int32(1), addr, int32(port), pkt.data, bindings.Size_t(pkt.dataLen), &ptr_id)
			if err != bindings.QUICLY_OK {
				if err != bindings.QUICLY_ERROR_PACKET_IGNORED {
					s.Logger.Error().Msgf("QUICLY Received %d bytes (failed processing %v)", pkt.dataLen, err)
				}
				continue
			}

			s.id = uint64(ptr_id)
			bindings.RegisterConnection(s, s.id)

		} else {
			retAddr := s.Conn.RemoteAddr()
			returnAddr = retAddr.(*net.UDPAddr)
		}

		num_packets = bindings.Size_t(128)
		packets_buf = make([]bindings.Iovec, 128)

		s.outLock.Lock()
		var ret = bindings.QuiclyOutgoingMsgQueue(bindings.Size_t(s.id), packets_buf, &num_packets)
		s.outLock.Unlock()
		if ret != bindings.QUICLY_OK || num_packets == 0 {
			continue
		}

		s.Logger.Debug().Msgf("QUICLY Sending %d packets", num_packets)

		addr, port := returnAddr.IP.String(), returnAddr.Port

		for i := 0; i < int(num_packets); i++ {
			s.outLock.Lock()
			packets_buf[i].Deref() // realize the struct copy from C -> go

			s.Logger.Debug().Msgf("QUICLY OUT packet to %s:%d, of %d bytes", addr, port, packets_buf[i].Iov_len)

			s.outgoingQueue = append(s.outgoingQueue, packet{
				data:    bindings.IovecToBytes(packets_buf[i]),
				dataLen: int(packets_buf[i].Iov_len),
				addr:    returnAddr,
			})
			s.outLock.Unlock()
		}
	}
}

func (s *QClientSession) connectionOutHandler() {
	var msgQueue = make([]packet, 0, 512)
	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-time.After(1 * time.Millisecond):
		default:
		}

		s.outLock.RLock()
		emptyCheck := len(s.outgoingQueue) == 0
		s.outLock.RUnlock()
		if emptyCheck {
			continue
		}

		s.outLock.Lock()
		var backupQueue = s.outgoingQueue
		s.outgoingQueue = msgQueue
		msgQueue = backupQueue
		s.outLock.Unlock()

		for len(msgQueue) >= 1 {
			n, err := s.Conn.Write(msgQueue[0].data)
			s.Logger.Debug().Msgf("SEND packet of len %d to %v [%v][%d]", msgQueue[0].dataLen, msgQueue[0].addr.String(),
				err, n)
			msgQueue = msgQueue[1:]
		}
	}
}

func (s *QClientSession) ID() uint64 {
	return s.id
}

func (s *QClientSession) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (s *QClientSession) Close() error {
	if !s.connected || s == nil || s.Conn == nil {
		return nil
	}
	if s.OnConnectionClose != nil {
		s.Logger.Debug().Msgf("Close connection: %d\n", s.id)
		s.outLock.Lock()
		s.OnConnectionClose(s)
		s.outLock.Unlock()
	}
	bindings.RemoveConnection(s.id)
	return nil
}

func (s *QClientSession) Addr() net.Addr {
	return s.Conn.RemoteAddr()
}

func (s *QClientSession) connect() int {
	if s.connected {
		return errors.QUICLY_OK
	}

	s.streams = make(map[uint64]types.Stream)

	var ptr_id bindings.Size_t = 0

	udpAddr := s.Addr().(*net.UDPAddr)

	if ret := bindings.QuiclyConnect(udpAddr.IP.String(), int32(udpAddr.Port), &ptr_id); ret != errors.QUICLY_OK {
		return int(ret)
	}

	s.id = uint64(ptr_id)
	bindings.RegisterConnection(s, s.id)

	s.connected = true

	go s.connectionInHandler()
	go s.connectionProcessHandler()
	go s.connectionOutHandler()

	if s.OnConnectionOpen != nil {
		s.OnConnectionOpen(s)
	}

	return errors.QUICLY_OK
}

func (s *QClientSession) OpenStream() types.Stream {
	if s.connect() != errors.QUICLY_OK {
		return nil
	}

	var ptr_id bindings.Size_t = 0

	if ret := bindings.QuiclyOpenStream(bindings.Size_t(s.id), &ptr_id); ret != errors.QUICLY_OK {
		return nil
	}

	streamId := uint64(ptr_id)

	st := &QStream{
		session: s,
		conn:    s.Conn,
		id:      streamId,
		Logger:  s.Logger,
	}
	st.closed.Store(false)

	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	s.streams[streamId] = st

	return st
}

func (s *QClientSession) GetStream(id uint64) types.Stream {
	s.streamsLock.Lock()
	defer s.streamsLock.Unlock()
	return s.streams[id]
}

func (s *QClientSession) OnStreamOpen(streamId uint64) {
	if s.OnStreamOpenCallback == nil {
		return
	}

	s.streamsLock.Lock()
	st, ok := s.streams[streamId]
	s.streamsLock.Unlock()
	if ok {
		s.OnStreamOpenCallback(st)
		st.OnOpened()
	}
}

func (s *QClientSession) OnStreamClose(streamId uint64, error int) {
	s.Logger.Info().Msgf("On close stream: %d\n", streamId)

	if s.OnStreamCloseCallback == nil {
		return
	}

	s.streamsLock.Lock()
	st, ok := s.streams[streamId]
	delete(s.streams, streamId)
	s.streamsLock.Unlock()

	if ok {
		s.OnStreamCloseCallback(st, error)
		_ = st.OnClosed()
	}
}

var _ net.Listener = &QClientSession{}
