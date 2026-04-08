package socket

import (
	"context"
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
}

// NewServer constructs a Server that dispatches requests to d.
func NewServer(d Dispatcher, log *slog.Logger, opts ...ServerOption) *Server {
	s := &Server{
		dispatcher:       d,
		log:              log,
		shutdownDeadline: 5 * time.Second,
		maxConns:         16,
		maxInFlight:      32,
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
