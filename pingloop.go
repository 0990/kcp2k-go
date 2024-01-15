package kcp2k

import "time"

const pingInterval = time.Millisecond * 1000
const PingTimeout = time.Second * 5

func (s *Session) pingLoop() {
	workSignal := make(chan struct{}, 1)

	time.AfterFunc(pingInterval, func() {
		workSignal <- struct{}{}
	})

	for {
		select {
		case <-s.die:
			return
		case <-workSignal:
			t := s.lastPingReceiveTime.Load().(time.Time)
			if time.Since(t) > PingTimeout {
				s.Close()
				return
			}

			s.sendReliable(Ping, nil)
			time.AfterFunc(pingInterval, func() {
				workSignal <- struct{}{}
			})
		}
	}
}
