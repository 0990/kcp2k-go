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

type KCPOutput interface {
	KCPOutput(data []byte)
}

// 封装给kcp-go UDPSession的Conn
// 1，mirror kcp有可靠传输和非可靠传输，在读取conn时，根据头部的第一个字节来识别,将识别为可靠传输的投喂给KcpUnderlyingConn(packetInput)
// 2，kcp-go UDPSession再通过ReadFrom来读取可靠流
// 3,kcp-go UDPSession通过WriteTo来输出流，mirror kcp需要增加头部自定义字段再发送出去，详见：Session.KCPOutput
type KcpUnderlyingConn struct {
	net.PacketConn
	chReadMessages chan KCPMessage

	socketReadError     atomic.Value
	chSocketReadError   chan struct{}
	socketReadErrorOnce sync.Once

	findKCPOut func(addr net.Addr) (KCPOutput, error)
}

func newKcpUnderlyingConn(conn net.PacketConn, findKcpOut func(addr net.Addr) (KCPOutput, error)) *KcpUnderlyingConn {
	c := new(KcpUnderlyingConn)
	c.PacketConn = conn
	c.chReadMessages = make(chan KCPMessage, KCPMessageLimit)
	c.chSocketReadError = make(chan struct{})
	c.findKCPOut = findKcpOut
	return c
}

func (c *KcpUnderlyingConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
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
func (c *KcpUnderlyingConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	o, err := c.findKCPOut(addr)
	if err != nil {
		return 0, err
	}
	o.KCPOutput(p)
	return len(p), nil
}

func (c *KcpUnderlyingConn) packetInput(data []byte, addr net.Addr) {
	c.chReadMessages <- KCPMessage{
		Data: data,
		Addr: addr,
	}
}

func (c *KcpUnderlyingConn) notifyReadError(err error) {
	c.socketReadErrorOnce.Do(func() {
		c.socketReadError.Store(err)
		close(c.chSocketReadError)
	})
}
