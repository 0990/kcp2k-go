package kcp2k

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"github.com/0990/kcp-go"
	"github.com/pkg/errors"
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

	conn    net.PacketConn // the underlying packet connection
	ownConn bool           // true if we created conn internally, false if provided by caller

	remote  net.Addr
	l       *Listener
	kcpSess *kcp.UDPSession
	cookie  []byte

	//当session为客户端时有值
	kcpConn *KcpUnderlyingConn

	rd     time.Time // read deadline
	bufptr []byte

	lastReceiveTime     time.Time
	chUnReliableReadMsg chan []byte
	chReliableReadMsg   chan []byte

	chAcceptKCPEvent       chan struct{}
	setKCPSessionEventOnce sync.Once

	socketReadError      atomic.Value
	socketWriteError     atomic.Value
	chSocketReadError    chan struct{}
	chSocketWriteError   chan struct{}
	socketReadErrorOnce  sync.Once
	socketWriteErrorOnce sync.Once

	die     chan struct{} // notify current session has Closed
	dieOnce sync.Once

	chTxQueue chan ipv4.Message

	lastPingReceiveTime atomic.Value

	mu sync.Mutex
}

func DialWithOptions(raddr string) (*Session, error) {
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	//这里使用ListenUDP,建立一个无连接的udp连接，方便tx发送时能使用WriteToUDP
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var convid uint32
	binary.Read(rand.Reader, binary.LittleEndian, &convid)
	s := newSession(nil, nil, conn, true, udpaddr)

	s.kcpConn = newKcpUnderlyingConn(conn, func(addr net.Addr) (KCPOutput, error) {
		return s, nil
	})

	kcpSess, err := kcp.NewConn3(convid, udpaddr, nil, 0, 0, s.kcpConn)
	if err != nil {
		return nil, err
	}
	ok := s.SetKcpSession(kcpSess)
	if !ok {
		return nil, err
	}
	s.sendReliable(Hello, nil)

	err = s.Run()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func newSession(cookie []byte, l *Listener, conn net.PacketConn, ownConn bool, addr net.Addr) *Session {
	s := new(Session)
	s.l = l
	s.conn = conn
	s.ownConn = ownConn
	s.cookie = cookie
	s.remote = addr
	s.chAcceptKCPEvent = make(chan struct{}, 1)
	s.die = make(chan struct{})
	s.chSocketReadError = make(chan struct{})
	s.chUnReliableReadMsg = make(chan []byte, 10)
	s.chReliableReadMsg = make(chan []byte, 10)
	s.chTxQueue = make(chan ipv4.Message, 10)

	if s.l == nil {
		go s.readLoop()
	}

	go s.sendLoop()
	return s
}

func (s *Session) SetState(state Kcp2kState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// 握手并接受数据
func (s *Session) Run() error {
	//握手
	s.kcpSess.SetReadDeadline(time.Now().Add(time.Second * 5))
	packet, err := ReadPacket(s.kcpSess)
	if err != nil {
		return err
	}
	s.kcpSess.SetReadDeadline(time.Time{})

	opCode, _, err := parseKcp2kBodyData(packet)
	if err != nil {
		return err
	}

	if opCode != Hello {
		return errors.New("not hello")
	}

	s.SetState(Authenticated)
	if s.l != nil {
		s.l.chAccepts <- s
		s.sendReliable(Hello, nil)
	}

	s.lastPingReceiveTime.Store(time.Now())
	go s.readKcpLoop()
	go s.pingLoop()

	return nil
}

func (s *Session) RemoteAddr() net.Addr { return s.remote }

func (c *Session) SetKcpSession(sess *kcp.UDPSession) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.kcpSess != nil {
		return false
	}
	c.kcpSess = sess
	c.setKCPSessionEventOnce.Do(func() {
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
		if s.l != nil {
			s.l.sessions.Delete(s.remote.String())
		}
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

// 读不可靠消息流
func (s *Session) onRawInputUnreliable(data []byte) {
	if s.state == Authenticated {
		s.chUnReliableReadMsg <- data
		s.lastReceiveTime = time.Now()
	} else {
		slog.Warn("Received unauthenticated data")
	}
}

func (s *Session) Read(b []byte) (n int, channel Channel, err error) {
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

// 读kcp可靠消息流：listener read raw->kcp input->readKcpLoop
func (s *Session) readKcpLoop() {
	go func() {
		for {
			data, err := ReadPacket(s.kcpSess)
			if err != nil {
				s.notifyReadError(err)
				return
			}
			err = s.handleKCPRawData(data)
			if err != nil {
				s.notifyReadError(err)
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
		slog.Debug("recv ping")
		s.lastPingReceiveTime.Store(time.Now())
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

func (s *Session) Send(data []byte, channel Channel) (int, error) {
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
	bts := xmitBuf.Get().([]byte)[:len(data)+headerSize]
	bts[0] = byte(Unreliable)
	copy(bts[1:], s.cookie)
	copy(bts[headerSize:], data)

	var msg ipv4.Message
	msg.Buffers = [][]byte{bts}
	msg.Addr = s.remote
	s.chTxQueue <- msg
	return
}

// kcp出口
func (s *Session) KCPOutput(data []byte) {
	var msg ipv4.Message
	bts := xmitBuf.Get().([]byte)[:len(data)+headerSize]
	bts[0] = byte(Reliable)
	copy(bts[1:], s.cookie)
	copy(bts[headerSize:], data)
	msg.Buffers = [][]byte{bts}
	msg.Addr = s.remote
	s.chTxQueue <- msg
}

func (s *Session) notifyReadError(err error) {
	s.socketReadErrorOnce.Do(func() {
		s.socketReadError.Store(err)
		close(s.chSocketReadError)
	})
}

func (s *Session) notifyWriteError(err error) {
	s.socketWriteErrorOnce.Do(func() {
		s.socketWriteError.Store(err)
		close(s.chSocketWriteError)
	})
}
