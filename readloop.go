package kcp2k

import (
	"github.com/pkg/errors"
	"log/slog"
	"net"
)

func (l *Listener) monitor() {
	l.defaultMonitor()
}

func (l *Listener) defaultMonitor() {
	buf := make([]byte, mtuLimit)
	for {
		if n, from, err := l.conn.ReadFrom(buf); err == nil {
			l.packetInput(buf[:n], from)
		} else {
			l.notifyReadError(errors.WithStack(err))
			return
		}
	}
}

func (s *Session) readLoop() {
	s.defaultReadLoop()
}

func (s *Session) defaultReadLoop() {
	buf := make([]byte, mtuLimit)
	var src string

	for {
		if n, addr, err := s.conn.ReadFrom(buf); err == nil {
			// make sure the packet is from the same source
			if src == "" { // set source address
				src = addr.String()
			} else if addr.String() != src {
				continue
			}
			s.packetInput(buf[:n], addr)
		} else {
			s.notifyReadError(errors.WithStack(err))
			return
		}

	}
}

// 当s为客户端时，自读取数据
func (s *Session) packetInput(data []byte, addr net.Addr) {
	if len(data) < headerSize {
		return
	}

	var channel = Channel(data[0])
	var cookie = data[1:headerSize]

	err := s.CheckCookie(cookie)
	if err != nil {
		slog.With("error", err).Warn("invalid cookie")
		return
	}

	switch channel {
	case Reliable:
		kcpData := data[headerSize:]
		s.kcpConn.packetInput(kcpData, addr)
	case Unreliable:
		s.onRawInputUnreliable(data[headerSize:])
		return
	default:

	}
}
