// server_options.go holds the functional-options surface for Server
// construction. Split from server.go (ARCH-04) along the same
// file-per-responsibility seam as accept.go / subscribe.go /
// connection.go — see the C-003 architecture note on the Server struct.
package socket

import "time"

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

// WithServerVersion sets the `serverVersion` value echoed to clients in the
// `system.hello` success response. The daemon must pass an ldflag-injected
// semver string (e.g. "1.2.3"); non-semver values are sanitised to "0.0.0"
// on the wire per FR-012.
func WithServerVersion(v string) ServerOption {
	return func(s *Server) { s.serverVersion = v }
}

// WithHelloBudget overrides the 5-second handshake deadline. Production
// callers should not use this; it exists for deterministic tests that need
// to exercise the timeout path without sleeping 5s.
func WithHelloBudget(d time.Duration) ServerOption {
	return func(s *Server) { s.helloBudget = d }
}

// WithEventReplay wires the durable event ring so `subscribe({since: N})`
// replays the buffered events with seq > N before going live (FR-061).
// Without it the cursor only suppresses; see replay.go.
func WithEventReplay(r EventReplayer) ServerOption {
	return func(s *Server) { s.replay = r }
}
