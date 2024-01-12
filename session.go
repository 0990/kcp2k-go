package kcp2k

import (
	"bytes"
	"fmt"
	"github.com/pkg/errors"
	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/net/ipv4"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Kcp2kState byte

const (
	Connected Kcp2kState = iota
	Authenticated
	Disconnected
)

var (
	// a system-wide packet buffer shared among sending, receiving and FEC
	// to mitigate high-frequency memory allocation for packets, bytes from xmitBuf
	// is aligned to 64bit
	xmitBuf sync.Pool
)

func init() {
	xmitBuf.New = func() interface{} {
		return make([]byte, mtuLimit)
	}
}

type Session struct {
	state Kcp2kState

	remote  net.Addr
	l       *Listener
	kcpSess *kcp.UDPSession
	cookie  []byte

	rd     time.Time // read deadline
	bufptr []byte

	lastReceiveTime     time.Time
	chUnReliableReadMsg chan []byte
	chReliableReadMsg   chan []byte

	chAcceptKCPEvent   chan struct{}
	acceptKCPEventOnce sync.Once

	socketReadError     atomic.Value
	chSocketReadError   chan struct{}
	socketReadErrorOnce sync.Once

	die     chan struct{} // notify current session has Closed
	dieOnce sync.Once

	txqueue []ipv4.Message

	mu sync.Mutex
}

func newSession(l *Listener, cookie []byte, addr net.Addr) *Session {
	s := new(Session)
	s.l = l
	s.cookie = cookie
	s.remote = addr
	s.chAcceptKCPEvent = make(chan struct{}, 1)
	s.die = make(chan struct{})
	s.chSocketReadError = make(chan struct{})
	s.chUnReliableReadMsg = make(chan []byte, 10)
	s.chReliableReadMsg = make(chan []byte, 10)
	return s
}

func (s *Session) SetState(state Kcp2kState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (c *Session) AcceptKcp(sess *kcp.UDPSession) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.kcpSess != nil {
		return false
	}
	c.kcpSess = sess
	c.acceptKCPEventOnce.Do(func() {
		close(c.chAcceptKCPEvent)
	})

	return true
}

func (s *Session) WaitAcceptKCP(cb func(error)) {
	go func() {
		select {
		case <-s.chAcceptKCPEvent:
			cb(nil)
			return
		case <-time.After(time.Second * 5):
			cb(errTimeout)
			return
		}
	}()
}

func (s *Session) Close() {
	var once bool
	s.dieOnce.Do(func() {
		close(s.die)
		once = true
	})

	if once {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.kcpSess != nil {
			s.kcpSess.Close()
		}
		s.l.sessions.Delete(s.remote.String())
	}
}

func (s *Session) CheckCookie(cookie []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.l == nil && s.cookie == nil {
		s.cookie = cookie
	}

	if s.state == Authenticated {
		if !bytes.Equal(cookie, s.cookie) {
			return fmt.Errorf("Invalid cookie,expected:%s,actual:%s", string(s.cookie), cookie)
		}
	}

	return nil
}

func (s *Session) onRawInputUnreliable(data []byte) {
	if s.state == Authenticated {
		s.chUnReliableReadMsg <- data
		s.lastReceiveTime = time.Now()
	} else {
		slog.Warn("Received unauthenticated data")
	}
}

func (s *Session) Read(b []byte) (n int, channel Kcp2kChannel, err error) {
	var timeout *time.Timer
	// deadline for current reading operation
	var c <-chan time.Time
	if !s.rd.IsZero() {
		delay := time.Until(s.rd)
		timeout = time.NewTimer(delay)
		c = timeout.C
		defer timeout.Stop()
	}

	for {
		s.mu.Lock()
		if len(s.bufptr) > 0 { // copy from buffer into b
			n = copy(b, s.bufptr)
			s.bufptr = s.bufptr[n:]
			s.mu.Unlock()
			return n, Reliable, nil
		}
		s.mu.Unlock()

		select {
		case data := <-s.chReliableReadMsg:
			s.mu.Lock()
			n = copy(b, data)   // copy to 'b'
			s.bufptr = data[n:] // pointer update
			s.mu.Unlock()
			return n, Reliable, nil
		case msg := <-s.chUnReliableReadMsg:
			if len(msg) > len(b) {
				return 0, Invalid, errors.New("buffer too small")
			}
			n = copy(b, msg)
			return n, Unreliable, nil
		case <-c:
			return 0, Invalid, errors.WithStack(errTimeout)
		case <-s.chSocketReadError:
			return 0, Invalid, s.socketReadError.Load().(error)
		case <-s.die:
			return 0, Invalid, errors.WithStack(io.ErrClosedPipe)
		}
	}
}

func (s *Session) readKcpLoop() {
	go func() {
		for {
			data := ReadPacket(s.kcpSess)
			err := s.handleKCPRawData(data)
			if err != nil {
				return
			}
		}
	}()
}

func (s *Session) handleKCPRawData(rawData []byte) error {
	opCode, data, err := parseKcp2kBodyData(rawData)
	if err != nil {
		return err
	}

	switch opCode {
	case Hello:
		s.Close()
		return errors.New("invalid hello message")
	case Ping:
		slog.Info("recv ping")
		return nil
	case Data:
		s.chReliableReadMsg <- data
		return nil
	case Disconnect:
		s.Close()
		return errors.WithStack(io.ErrClosedPipe)
	default:
		s.Close()
		return errors.WithStack(io.ErrClosedPipe)
	}
}

func (s *Session) Send(data []byte, channel Kcp2kChannel) (int, error) {
	switch channel {
	case Reliable:
		return s.sendReliable(Data, data)
	case Unreliable:
		s.sendUnReliable(data)
		return 0, nil
	default:
		return 0, errors.New("invalid channel")
	}
}

func (s *Session) sendReliable(opcode Kcp2kOpcode, data []byte) (int, error) {
	return s.kcpSess.Write(append([]byte{byte(opcode)}, data...))
}

func (s *Session) sendUnReliable(data []byte) {
	bts := xmitBuf.Get().([]byte)[:len(data)+kcp2kHeaderSize]
	bts[0] = byte(Unreliable)
	copy(bts[1:], s.cookie)
	copy(bts[kcp2kHeaderSize:], data)

	var msg ipv4.Message
	msg.Buffers = [][]byte{bts}
	msg.Addr = s.remote
	s.txqueue = append(s.txqueue, msg)
	return
}

// kcp出口
func (s *Session) KCPOutput(data []byte) {
	var msg ipv4.Message
	bts := xmitBuf.Get().([]byte)[:len(data)+kcp2kHeaderSize]
	bts[0] = byte(Reliable)
	copy(bts[1:], s.cookie)
	copy(bts[kcp2kHeaderSize:], data)
	msg.Buffers = [][]byte{bts}
	msg.Addr = s.remote
	s.txqueue = append(s.txqueue, msg)
}

func (s *Session) notifyReadError(err error) {
	s.socketReadErrorOnce.Do(func() {
		s.socketReadError.Store(err)
		close(s.chSocketReadError)
	})
}
