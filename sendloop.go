package kcp2k

import "github.com/pkg/errors"

func (s *Session) sendLoop() {
	for tx := range s.chTxQueue {
		if _, err := s.conn.WriteTo(tx.Buffers[0], tx.Addr); err == nil {
			xmitBuf.Put(tx.Buffers[0])
		} else {
			s.notifyWriteError(errors.WithStack(err))
			break
		}
	}
}
