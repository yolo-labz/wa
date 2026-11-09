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
	snapshot := s.snapshotConns()

	for _, c := range snapshot {
		pushOutFrames(c, evt, collectOutFrames(c, evt))
	}
}

// outFrame is one frame queued for a connection during fan-out.
type outFrame struct {
	frame []byte
	sub   *Subscription // nil for drop frames (no lastSeq advance)
}

// collectOutFrames builds, under c.mu, the frames evt produces for c: a
// stream.drop frame on a seq gap, then the matched event frame. Pushing
// happens outside the lock: pushNotification's backpressure path writes to
// the raw socket, and holding c.mu across that write lets one slow peer
// stall this serial fan-out loop for every other connection (CON-03).
func collectOutFrames(c *Connection, evt Event) []outFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	return frames
}

// pushOutFrames pushes frames to c outside c.mu. lastSeq is re-locked per
// successful push — safe because fan-out is the only goroutine that
// advances it.
func pushOutFrames(c *Connection, evt Event, frames []outFrame) {
	for _, f := range frames {
		err := c.pushNotification(f.frame)
		if err == nil && f.sub != nil && evt.Seq > 0 {
			c.mu.Lock()
			f.sub.lastSeq = evt.Seq
			c.mu.Unlock()
		}
	}
}

// sendSubscriptionClosed sends a -32005 SubscriptionClosed error notification
// to every connection that has active subscriptions, then clears the
// subscriptions.
func (s *Server) sendSubscriptionClosed() {
	snapshot := s.snapshotConns()

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
