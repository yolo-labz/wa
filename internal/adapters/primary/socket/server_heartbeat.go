// server_heartbeat.go is the FR-062 liveness half of the Server: the
// ping/pong heartbeat loop and the pong-timeout reaper. Split from
// server.go (ARCH-04) — see the C-003 architecture note on the Server
// struct.
package socket

import "time"

// heartbeatLoop emits subscribe.ping notifications every pingInterval and
// reaps subscriptions whose lastPongAt exceeds pongTimeout. Exits when the
// server context is cancelled.
func (s *Server) heartbeatLoop() {
	t := time.NewTicker(s.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.tickHeartbeat(time.Now())
		}
	}
}

// tickHeartbeat runs one heartbeat cycle: emit pings, reap pong-timed-out
// subscriptions. Exposed for synctest-driven tests.
func (s *Server) tickHeartbeat(now time.Time) {
	s.connsMu.Lock()
	conns := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.connsMu.Unlock()

	for _, c := range conns {
		c.mu.Lock()
		subs := make([]*Subscription, 0, len(c.subscriptions))
		for _, sub := range c.subscriptions {
			subs = append(subs, sub)
		}
		c.mu.Unlock()

		for _, sub := range subs {
			c.mu.Lock()
			overdue := s.pongTimeout > 0 && now.Sub(sub.lastPongAt) > s.pongTimeout
			lastSeq := sub.lastSeq
			c.mu.Unlock()

			if overdue {
				closedFrame := subscribeClosedPongTimeoutFrame(sub.id, lastSeq)
				_ = c.pushNotification(closedFrame)
				c.mu.Lock()
				delete(c.subscriptions, sub.id)
				c.mu.Unlock()
				continue
			}
			pingFrame := subscribePingFrame(sub.id)
			_ = c.pushNotification(pingFrame)
		}
	}
}
