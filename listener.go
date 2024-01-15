package kcp2k

import (
	"github.com/0990/kcp-go"
	"github.com/0990/kcp2k-go/pkg/syncx"
	"github.com/pkg/errors"
	"io"
	"log"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	mtuLimit      = 1500
	acceptBacklog = 128
)

type Listener struct {
	conn net.PacketConn

	kcpConn *KcpUnderlyingConn

	sessions syncx.Map[string, *Session]

	chAccepts       chan *Session // Listen() backlog
	chSessionClosed chan net.Addr // session close queue

	die     chan struct{} // notify the listener has closed
	dieOnce sync.Once

	// socket error handling
	socketReadError     atomic.Value
	chSocketReadError   chan struct{}
	socketReadErrorOnce sync.Once

	rd atomic.Value // read deadline for Accept()
}

func ListenWithOptions(laddr string) (*Listener, error) {
	udpaddr, err := net.ResolveUDPAddr("udp", laddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	conn, err := net.ListenUDP("udp", udpaddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return serveConn(conn)
}

func serveConn(conn net.PacketConn) (*Listener, error) {
	l := new(Listener)
	l.conn = conn
	l.kcpConn = newKcpUnderlyingConn(conn, func(addr net.Addr) (KCPOutput, error) {
		sess, ok := l.sessions.Load(addr.String())
		if !ok {
			return nil, errors.New("no session")
		}
		return sess, nil
	})
	l.listenKCP()

	l.chAccepts = make(chan *Session, acceptBacklog)
	l.chSessionClosed = make(chan net.Addr)
	l.die = make(chan struct{})
	l.chSocketReadError = make(chan struct{})
	go l.monitor()

	return l, nil
}

func (l *Listener) listenKCP() (*kcp.Listener, error) {
	kcpListener, err := kcp.ServeConn(nil, 0, 0, l.kcpConn)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			s, err := kcpListener.AcceptKCP()
			if err != nil {
				log.Fatal(err)
			}
			go func() {
				err := l.handleNewKcp(s)
				if err != nil {
					slog.Warn("handleNewKcp error", "error", err)
				}
			}()
		}
	}()
	return kcpListener, nil
}

func (l *Listener) handleNewKcp(sess *kcp.UDPSession) error {
	addr := sess.RemoteAddr().String()

	s, _ := l.sessions.Load(addr)
	if s == nil {
		return errors.New("s==nil")
	}

	ok := s.SetKcpSession(sess)
	if !ok {
		l.sessions.Delete(addr)
		return errors.New("s.kcpSess!=nil")
	}

	err := s.Run()
	if err != nil {
		return err
	}
	return nil
}

func ReadPacket(session *kcp.UDPSession) ([]byte, error) {
	return session.ReadPacket()
}

func (l *Listener) Accept() (*Session, error) {
	var timeout <-chan time.Time
	if tdeadline, ok := l.rd.Load().(time.Time); ok && !tdeadline.IsZero() {
		timeout = time.After(time.Until(tdeadline))
	}

	select {
	case <-timeout:
		return nil, errors.WithStack(errTimeout)
	case c := <-l.chAccepts:
		return c, nil
	case <-l.chSocketReadError:
		return nil, l.socketReadError.Load().(error)
	case <-l.die:
		return nil, errors.WithStack(io.ErrClosedPipe)
	}
}

func (l *Listener) notifyReadError(err error) {
	l.socketReadErrorOnce.Do(func() {
		l.socketReadError.Store(err)
		close(l.chSocketReadError)
		l.sessions.Range(func(key string, sess *Session) bool {
			sess.notifyReadError(err)
			return true
		})
	})
}
