package kcp2k

import (
	"github.com/pkg/errors"
	"net"
	"sync"
	"sync/atomic"
)

var (
	errInvalidOperation = errors.New("invalid operation")
	errTimeout          = errors.New("timeout")
	errBufferSmall      = errors.New("buffsmall")
)

const (
	KCPMessageLimit = 128
)

type KCPMessage struct {
	Data []byte
	Addr net.Addr
}

type KcpServerConn struct {
	net.PacketConn
	l              *Listener
	chReadMessages chan KCPMessage

	socketReadError     atomic.Value
	chSocketReadError   chan struct{}
	socketReadErrorOnce sync.Once
}

func newKcpServerConn(conn net.PacketConn, l *Listener) *KcpServerConn {
	c := new(KcpServerConn)
	c.PacketConn = conn
	c.chReadMessages = make(chan KCPMessage, KCPMessageLimit)
	c.chSocketReadError = make(chan struct{})
	return c
}

func (c *KcpServerConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	select {
	case msg := <-c.chReadMessages:
		data := msg.Data
		if len(p) < len(data) {
			return 0, nil, errors.WithStack(errBufferSmall)
		}
		n = copy(p, data)
		return n, msg.Addr, nil
	}
}

// mirror kcp需要加头
func (c *KcpServerConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	sess, ok := c.l.sessions.Load(addr.String())
	if !ok {
		return 0, errors.New("sess not exit")
	}
	sess.KCPOutput(p)
	return len(p), nil
}

func (c *KcpServerConn) packetInput(data []byte, addr net.Addr) {
	c.chReadMessages <- KCPMessage{
		Data: data,
		Addr: addr,
	}
}

func (c *KcpServerConn) notifyReadError(err error) {
	c.socketReadErrorOnce.Do(func() {
		c.socketReadError.Store(err)
		close(c.chSocketReadError)
	})
}
