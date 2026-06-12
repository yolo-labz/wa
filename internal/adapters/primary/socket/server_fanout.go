// server_fanout.go is the event fan-out half of the Server: the goroutine
// that drains the dispatcher's Events() channel and delivers matched
// frames to subscribed connections. Split from server.go (ARCH-04) — see
// the C-003 architecture note on the Server struct.
package socket

// eventFanOut reads events from the dispatcher's Events() channel and fans
// them out to all connections that have matching subscriptions. When the
// Events() channel closes, it sends a -32005 SubscriptionClosed notification
// to every connection with active subscriptions. It also exits when the
// server context is cancelled.
func (s *Server) eventFanOut() {
	events := s.dispatcher.Events()
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				// Events channel closed — notify all subscribers.
				s.sendSubscriptionClosed()
				return
			}
			s.fanOutEvent(evt)
		case <-s.ctx.Done():
			return
		}
	}
}

// fanOutEvent delivers a single event to all connections whose subscriptions
// match the filter DSL (FR-060). If the event's Seq reveals a ring-buffer
// gap vs. the subscription's lastSeq, a stream.drop frame is emitted first
// (FR-063) before the matched event frame, and lastSeq is advanced.
func (s *Server) fanOutEvent(evt Event) {
	s.connsMu.Lock()
	snapshot := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		snapshot = append(snapshot, c)
	}
	s.connsMu.Unlock()

	// Per-connection: build the frame list under c.mu, push outside it.
	// pushNotification's backpressure path writes to the raw socket;
	// holding c.mu across that write lets one slow peer stall this
	// serial fan-out loop for every other connection (CON-03). lastSeq
	// is re-locked per successful push — safe because this is the only
	// goroutine that advances it.
	type outFrame struct {
		frame []byte
		sub   *Subscription // nil for drop frames (no lastSeq advance)
	}
	for _, c := range snapshot {
		c.mu.Lock()
		frames := make([]outFrame, 0, len(c.subscriptions))
		for _, sub := range c.subscriptions {
			if !matchesSub(sub, evt, sub.bodyReCompiled) {
				continue
			}
			// Gap detection: only when both sides are seq-aware.
			if evt.Seq > 0 && sub.lastSeq > 0 && evt.Seq > sub.lastSeq+1 {
				dropFrame := streamDropFrame(sub.id, sub.lastSeq+1, evt.Seq-1)
				frames = append(frames, outFrame{frame: dropFrame})
			}
			frame, err := marshalNotification(evt, sub.id)
			if err != nil {
				c.log.Error("failed to marshal notification", "error", err)
				continue
			}
			frames = append(frames, outFrame{frame: frame, sub: sub})
		}
		c.mu.Unlock()

		for _, f := range frames {
			err := c.pushNotification(f.frame)
			if err == nil && f.sub != nil && evt.Seq > 0 {
				c.mu.Lock()
				f.sub.lastSeq = evt.Seq
				c.mu.Unlock()
			}
		}
	}
}

// sendSubscriptionClosed sends a -32005 SubscriptionClosed error notification
// to every connection that has active subscriptions, then clears the
// subscriptions.
func (s *Server) sendSubscriptionClosed() {
	s.connsMu.Lock()
	snapshot := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		snapshot = append(snapshot, c)
	}
	s.connsMu.Unlock()

	for _, c := range snapshot {
		c.mu.Lock()
		for subID := range c.subscriptions {
			frame := subscriptionClosedFrame(subID)
			_ = c.pushNotification(frame)
		}
		// Release all subscriptions.
		c.subscriptions = make(map[string]*Subscription)
		c.mu.Unlock()
	}
}
