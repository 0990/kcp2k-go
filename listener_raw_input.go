package kcp2k

import (
	"github.com/0990/kcp2k-go/pkg/util"
	"log/slog"
	"net"
)

const (
	kcp2kHeaderSize = 5
)

type Kcp2kChannel byte

const (
	Invalid    Kcp2kChannel = 0
	Reliable   Kcp2kChannel = 1
	Unreliable Kcp2kChannel = 2
)

// 原始udp数据输入
func (l *Listener) packetInput(data []byte, addr net.Addr) {
	if len(data) < kcp2kHeaderSize {
		return
	}

	addrStr := addr.String()
	s, _ := l.sessions.Load(addrStr)

	var channel = Kcp2kChannel(data[0])
	var cookie = data[1:kcp2kHeaderSize]
	if s != nil {
		err := s.CheckCookie(cookie)
		if err != nil {
			slog.With(err).Warn("invalid cookie")
			return
		}
	}

	switch channel {
	case Reliable:
		kcpData := data[kcp2kHeaderSize:]
		l.kcpConn.packetInput(kcpData, addr)

		if s == nil {
			s := newSession(l, util.RandBytes(4), addr)
			l.sessions.Store(addrStr, s)

			s.WaitAcceptKCP(func(err error) {
				if err != nil {
					s.Close()
					slog.Warn("handshake failed: %v", err)
					return
				}
			})
		}
	case Unreliable:
		if s != nil {
			s.onRawInputUnreliable(data[kcp2kHeaderSize:])
		}
		return
	default:

	}
}
