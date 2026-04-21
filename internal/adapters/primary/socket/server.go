package socket

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ServerOption configures a Server at construction time.
type ServerOption func(*Server)

// WithShutdownDeadline sets the maximum time the server waits for in-flight
// requests to complete after shutdown is initiated. Default: 5s.
func WithShutdownDeadline(d time.Duration) ServerOption {
	return func(s *Server) { s.shutdownDeadline = d }
}

// WithMaxConns sets the soft cap on concurrent connections. Default: 16.
func WithMaxConns(n int) ServerOption {
	return func(s *Server) { s.maxConns = n }
}

// WithMaxInFlight sets the per-connection in-flight request cap. Default: 32.
func WithMaxInFlight(n int) ServerOption {
	return func(s *Server) { s.maxInFlight = n }
}

// WithHeartbeat sets the ping cadence and pong deadline (FR-062).
// Defaults: ping=15s, pongTimeout=30s. A pongTimeout of 0 disables the reaper.
func WithHeartbeat(ping, pongTimeout time.Duration) ServerOption {
	return func(s *Server) {
		s.pingInterval = ping
		s.pongTimeout = pongTimeout
	}
}

// Server is the JSON-RPC 2.0 socket adapter. It owns the unix domain socket
// listener, the single-instance lock, and the per-connection goroutine pool.
// A Server cannot be restarted; construct a fresh one.
//
// Architecture note (016-code-quality-audit, C-003): 15 fields, all tightly
// related to socket server lifecycle.  Connection registry methods (addConn,
// removeConn, cancelAllConns, closeAllReads) already use copy-under-lock and
// are short.  Extracting shutdownCoordinator/connRegistry sub-structs was
// evaluated and rejected — it adds indirection without reducing cognitive load.
// The method set is in server.go, accept.go, subscribe.go, connection.go.
type Server struct {
	path        string
	listener    net.Listener
	lockRelease func()
	dispatcher  Dispatcher
	log         *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	connCounter      atomic.Uint64
	shutdownDeadline time.Duration
	maxConns         int
	maxInFlight      int
	pingInterval     time.Duration
	pongTimeout      time.Duration

	// shutdownStarted is set to true when graceful shutdown begins.
	// Checked in the dispatch path to reject new requests with -32002.
	shutdownStarted atomic.Bool

	// done is closed when Run() has fully completed (cleanup done).
	done chan struct{}

	// conns tracks all active connections, keyed by connection id.
	// Protected by connsMu.
	conns   map[uint64]*Connection
	connsMu sync.Mutex
}

// NewServer constructs a Server that dispatches requests to d.
func NewServer(d Dispatcher, log *slog.Logger, opts ...ServerOption) *Server {
	s := &Server{
		dispatcher:       d,
		log:              log,
		shutdownDeadline: 5 * time.Second,
		maxConns:         16,
		maxInFlight:      32,
		pingInterval:     15 * time.Second,
		pongTimeout:      30 * time.Second,
		conns:            make(map[uint64]*Connection),
		done:             make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run acquires the single-instance lock, starts listening, runs the accept
// loop, and blocks until ctx is cancelled. On clean shutdown it closes the
// listener, waits for connections to drain (up to shutdownDeadline), sends
// shutdown notifications to subscribers, removes the socket, and releases
// the lock.
func (s *Server) Run(ctx context.Context, socketPath string) error {
	// Acquire single-instance lock.
	release, err := Acquire(socketPath)
	if err != nil {
		return err
	}
	s.lockRelease = release
	s.path = socketPath

	// Create listener (runs pre-flight checks).
	ln, err := listen(socketPath)
	if err != nil {
		release()
		return err
	}
	s.listener = ln

	// Derive a cancellable context for the server lifetime.
	s.ctx, s.cancel = context.WithCancel(ctx)

	s.log.Info("server listening", "path", socketPath)

	// Start event fan-out goroutine.
	s.wg.Go(func() {
		s.eventFanOut()
	})

	// Start heartbeat goroutine (ping + reaper). FR-062.
	if s.pingInterval > 0 {
		s.wg.Go(func() {
			s.heartbeatLoop()
		})
	}

	// Start accept loop in a goroutine.
	s.wg.Go(func() {
		s.acceptLoop()
	})

	// Block until context is cancelled.
	<-s.ctx.Done()

	s.log.Info("graceful shutdown initiated")
	s.shutdownStarted.Store(true)

	// Close listener (causes acceptLoop to exit).
	if err := s.listener.Close(); err != nil {
		s.log.Warn("listener close error", "error", err)
	}

	// Send shutdown notification to all active subscribers.
	s.sendShutdownNotifications()

	// Close the read side of all connections so jrpc2 stops accepting new
	// requests, but keep the write side open so in-flight responses can be
	// flushed. This causes jrpc2's channel reader to see EOF, which triggers
	// it to finish in-flight requests and return from srv.Wait().
	s.closeAllReads()

	// Wait for connections to drain, with a deadline.
	drainDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drainDone)
	}()

	select {
	case <-drainDone:
		// All connections drained within deadline.
	case <-time.After(s.shutdownDeadline):
		// Deadline expired — force-cancel all remaining connection contexts.
		s.log.Warn("shutdown deadline expired, cancelling remaining connections",
			"deadline", s.shutdownDeadline,
		)
		s.cancelAllConns()
		// Wait for goroutines to finish after cancellation.
		<-drainDone
	}

	// Post-shutdown cleanup.
	// Remove socket file (ignore ENOENT). Never remove the .lock sibling.
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		s.log.Warn("socket remove error", "error", err, "path", s.path)
	}

	// Release single-instance lock.
	if s.lockRelease != nil {
		s.lockRelease()
		s.lockRelease = nil
	}

	s.log.Info("server stopped")
	close(s.done)
	return nil
}

// Shutdown initiates graceful shutdown by cancelling the server context.
func (s *Server) Shutdown() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Wait blocks until the server has fully shut down (all goroutines exited,
// socket removed, lock released). Call after Shutdown or after Run returns.
func (s *Server) Wait() error {
	<-s.done
	return nil
}

// ShutdownStarted reports whether graceful shutdown is in progress.
// Used by the dispatch path to reject new requests with -32002.
func (s *Server) ShutdownStarted() bool {
	return s.shutdownStarted.Load()
}

// addConn registers a connection in the server's connection map.
func (s *Server) addConn(c *Connection) {
	s.connsMu.Lock()
	s.conns[c.id] = c
	s.connsMu.Unlock()
}

// removeConn unregisters a connection from the server's connection map.
func (s *Server) removeConn(c *Connection) {
	s.connsMu.Lock()
	delete(s.conns, c.id)
	s.connsMu.Unlock()
}

// cancelAllConns cancels every active connection's context and closes the
// raw socket, causing their jrpc2 servers to shut down and in-flight
// requests to be cancelled.
func (s *Server) cancelAllConns() {
	s.connsMu.Lock()
	snapshot := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		snapshot = append(snapshot, c)
	}
	s.connsMu.Unlock()

	for _, c := range snapshot {
		c.cancel()
		_ = c.raw.Close()
	}
}

// closeAllReads closes the read side of every active connection, causing
// jrpc2's line reader to see EOF. The write side remains open so in-flight
// responses can be flushed before the connection fully closes.
func (s *Server) closeAllReads() {
	s.connsMu.Lock()
	snapshot := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		snapshot = append(snapshot, c)
	}
	s.connsMu.Unlock()

	for _, c := range snapshot {
		_ = c.raw.CloseRead()
	}
}

// sendShutdownNotifications sends a -32002 ShutdownInProgress error frame to
// every connection that has active subscriptions, as a final notification
// before the connection is closed.
func (s *Server) sendShutdownNotifications() {
	s.connsMu.Lock()
	snapshot := make([]*Connection, 0, len(s.conns))
	for _, c := range s.conns {
		snapshot = append(snapshot, c)
	}
	s.connsMu.Unlock()

	for _, c := range snapshot {
		c.mu.Lock()
		for subID := range c.subscriptions {
			frame := shutdownFrame(subID)
			_ = c.pushNotification(frame)
		}
		c.mu.Unlock()
	}
}

// shutdownFrame returns a JSON-RPC error notification for ShutdownInProgress (-32002).
func shutdownFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeShutdownInProgress),
			"message": errCodeName[CodeShutdownInProgress],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

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

	for _, c := range snapshot {
		c.mu.Lock()
		for _, sub := range c.subscriptions {
			if !matchesSub(sub, evt, sub.bodyReCompiled) {
				continue
			}
			// Gap detection: only when both sides are seq-aware.
			if evt.Seq > 0 && sub.lastSeq > 0 && evt.Seq > sub.lastSeq+1 {
				dropFrame := streamDropFrame(sub.id, sub.lastSeq+1, evt.Seq-1)
				_ = c.pushNotification(dropFrame)
			}
			frame, err := marshalNotification(evt, sub.id)
			if err != nil {
				c.log.Error("failed to marshal notification", "error", err)
				continue
			}
			if err := c.pushNotification(frame); err == nil && evt.Seq > 0 {
				sub.lastSeq = evt.Seq
			}
		}
		c.mu.Unlock()
	}
}

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

// subscribePingFrame returns a JSON-RPC notification asking the client to
// acknowledge liveness via subscribe.pong.
func subscribePingFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"method":  "subscribe.ping",
		"params": map[string]any{
			"subscriptionId": subscriptionID,
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// subscribeClosedPongTimeoutFrame returns a JSON-RPC error notification
// signalling FR-062 pong-timeout closure. resumeSince is the subscription's
// lastSeq at the moment of closure so the client can re-subscribe with a
// Kafka-style cursor.
func subscribeClosedPongTimeoutFrame(subscriptionID string, resumeSince int64) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodePongTimeout),
			"message": errCodeName[CodePongTimeout],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
				"reason":         "pong_timeout",
				"resumeSince":    resumeSince,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// streamDropFrame returns a JSON-RPC error notification signalling FR-063
// ring-buffer gap. count is the number of dropped events inclusive.
func streamDropFrame(subscriptionID string, oldestDropped, newestDropped int64) []byte {
	count := newestDropped - oldestDropped + 1
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeStreamDrop),
			"message": errCodeName[CodeStreamDrop],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
				"count":          count,
				"oldest_dropped": oldestDropped,
				"newest_dropped": newestDropped,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
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

// marshalNotification creates a JSON-RPC 2.0 server notification frame for
// an event.
func marshalNotification(evt Event, subscriptionID string) ([]byte, error) {
	params := map[string]any{
		"schema":         "wa.event/v1",
		"type":           evt.Type,
		"subscriptionId": subscriptionID,
	}
	if evt.Seq > 0 {
		params["seq"] = evt.Seq
	}
	frame := map[string]any{
		"jsonrpc": "2.0",
		"method":  "event",
		"params":  params,
	}
	return json.Marshal(frame)
}

// subscriptionClosedFrame returns a JSON-RPC error notification for
// SubscriptionClosed (-32005).
func subscriptionClosedFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeSubscriptionClosed),
			"message": errCodeName[CodeSubscriptionClosed],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}
