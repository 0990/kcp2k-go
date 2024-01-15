package kcp2k

import (
	"github.com/0990/kcp2k-go/pkg/util"
	"log/slog"
	"net"
)

const (
	headerSize = 5
)

type Channel byte

const (
	Invalid    Channel = 0
	Reliable   Channel = 1
	Unreliable Channel = 2
)

// 原始udp数据输入
func (l *Listener) packetInput(data []byte, addr net.Addr) {
	if len(data) < headerSize {
		return
	}

	addrStr := addr.String()
	s, _ := l.sessions.Load(addrStr)

	var channel = Channel(data[0])
	var cookie = data[1:headerSize]
	if s != nil {
		err := s.CheckCookie(cookie)
		if err != nil {
			slog.Warn("invalid cookie", "error", err)
			return
		}
	}

	switch channel {
	case Reliable:
		kcpData := data[headerSize:]
		l.kcpConn.packetInput(kcpData, addr)

		if s == nil {
			s := newSession(util.RandBytes(4), l, l.conn, false, addr)
			l.sessions.Store(addrStr, s)

			s.WaitAcceptKCP(func(err error) {
				if err != nil {
					s.Close()
					slog.Warn("handshake failed", "error", err)
					return
				}
			})
		}
	case Unreliable:
		if s != nil {
			s.onRawInputUnreliable(data[headerSize:])
		}
		return
	default:

	}
}
