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

// Server is the JSON-RPC 2.0 socket adapter. It owns the unix domain socket
// listener, the single-instance lock, and the per-connection goroutine pool.
// A Server cannot be restarted; construct a fresh one.
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
		conns:            make(map[uint64]*Connection),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run acquires the single-instance lock, starts listening, runs the accept
// loop, and blocks until ctx is cancelled. On clean shutdown it closes the
// listener, waits for connections to drain, removes the socket, and releases
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
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.eventFanOut()
	}()

	// Start accept loop in a goroutine.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop()
	}()

	// Block until context is cancelled.
	<-s.ctx.Done()

	// Shutdown sequence: close listener, wait for connections, cleanup.
	s.listener.Close()
	s.wg.Wait()

	// Remove socket file (ignore ENOENT).
	_ = os.Remove(s.path)

	// Release single-instance lock.
	if s.lockRelease != nil {
		s.lockRelease()
		s.lockRelease = nil
	}

	s.log.Info("server stopped")
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
	s.wg.Wait()
	return nil
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
// match the event type.
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
			if _, ok := sub.events[evt.Type]; ok {
				frame, err := marshalNotification(evt, sub.id)
				if err != nil {
					c.log.Error("failed to marshal notification", "error", err)
					continue
				}
				// pushNotification is non-blocking; on backpressure it closes
				// the connection.
				_ = c.pushNotification(frame)
			}
		}
		c.mu.Unlock()
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

// marshalNotification creates a JSON-RPC 2.0 server notification frame for
// an event.
func marshalNotification(evt Event, subscriptionID string) ([]byte, error) {
	params := map[string]any{
		"schema":         "wa.event/v1",
		"type":           evt.Type,
		"subscriptionId": subscriptionID,
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
