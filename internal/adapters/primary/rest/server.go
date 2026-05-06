package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// JSON-RPC 2.0 envelope for inbound and outbound frames. Mirrors the
// shapes the socket primary adapter handles — same wire format, just
// over HTTPS instead of unix socket.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxRequestBodyBytes caps an inbound JSON-RPC envelope. Mirrors the
// socket adapter's CodeOversizedMessage gate at 1 MiB (frame size).
// Defends against memory-exhaustion via crafted Content-Length.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// Server is the REST primary adapter. It wraps a Dispatcher with
// HTTP plumbing: bearer-token auth, JSON-RPC envelope decoding/
// encoding, request size limits, and a 503 short-circuit during
// graceful shutdown. Spec 110a.
type Server struct {
	dispatcher Dispatcher
	auth       Authenticator
	log        *slog.Logger
	srv        *http.Server
	listener   net.Listener
}

// ServerOption tunes a Server before listen.
type ServerOption func(*Server)

// WithLogger overrides the default slog logger.
func WithLogger(log *slog.Logger) ServerOption {
	return func(s *Server) {
		if log != nil {
			s.log = log
		}
	}
}

// NewServer constructs a Server. addr is bound synchronously (so
// port-already-in-use errors surface to the caller — same pattern as
// cmd/wad/health_http.go). Returns the bound listener address via
// ListenerAddr() for tests. Auth must NOT be nil; use
// NewEnvTokenAuth("") for an explicit deny-all stub.
func NewServer(ctx context.Context, addr string, dispatcher Dispatcher, auth Authenticator, opts ...ServerOption) (*Server, error) {
	if dispatcher == nil {
		return nil, errors.New("rest: NewServer requires a non-nil Dispatcher")
	}
	if auth == nil {
		return nil, errors.New("rest: NewServer requires a non-nil Authenticator")
	}
	s := &Server{
		dispatcher: dispatcher,
		auth:       auth,
		log:        slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/rpc", s.handleRPC)
	mux.HandleFunc("GET /v1/version", s.handleVersion)

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("rest: listen %s: %w", addr, err)
	}

	s.listener = listener
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	return s, nil
}

// Serve blocks until the listener returns. Call in a goroutine.
// Returns nil on graceful shutdown, the underlying error otherwise.
func (s *Server) Serve() error {
	err := s.srv.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ListenerAddr returns the bound TCP address (after port-zero
// resolution). Used by tests to discover the actual port.
func (s *Server) ListenerAddr() net.Addr {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Shutdown gracefully stops the HTTP server. Safe on a nil receiver.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// handleRPC implements POST /v1/rpc. Decodes the JSON-RPC envelope,
// authenticates, dispatches to Dispatcher.Handle, encodes the
// response. Always returns valid JSON-RPC envelopes — even on auth
// failure, the body is a JSON object with `error.code = -32099`
// (custom REST-layer code) so clients can parse uniformly.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if err := s.auth.Verify(r); err != nil {
		s.writeError(w, nil, http.StatusUnauthorized, -32099, "unauthorized")
		return
	}

	if r.Header.Get("Content-Type") != "application/json" {
		s.writeError(w, nil, http.StatusUnsupportedMediaType, -32700, "Content-Type must be application/json")
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeError(w, nil, http.StatusRequestEntityTooLarge, -32004, "request body exceeds 1 MiB")
			return
		}
		s.writeError(w, nil, http.StatusBadRequest, -32700, "read body: "+err.Error())
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeError(w, nil, http.StatusBadRequest, -32700, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, http.StatusBadRequest, -32600, `"jsonrpc" field must be "2.0"`)
		return
	}
	if req.Method == "" {
		s.writeError(w, req.ID, http.StatusBadRequest, -32600, `"method" field is required`)
		return
	}

	result, dispatchErr := s.dispatcher.Handle(r.Context(), req.Method, req.Params)
	if dispatchErr != nil {
		// Map dispatcher error to the REST envelope. The dispatcher's
		// typed errors carry an RPCCode interface that the socket
		// adapter unwraps; we intentionally do not link to that
		// translation table here — the REST surface returns a
		// generic -32603 Internal error and lets the caller branch
		// on HTTP 200 OK + envelope.error vs HTTP 4xx for protocol
		// failures. Future spec (110b/c) can map specific dispatcher
		// codes once the wire surface stabilises.
		s.writeError(w, req.ID, http.StatusOK, codeFromError(dispatchErr), dispatchErr.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	})
}

// handleVersion is unauthenticated and returns the daemon version.
// Useful for clients to verify they're talking to the right daemon
// before sending a token.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema":  "wa.rest.version/v1",
		"adapter": "rest-110a",
	})
}

// writeError emits a JSON-RPC error envelope at the given HTTP status.
// id may be nil for parse-stage errors where the envelope id is
// unknown.
func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, status, code int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: code, Message: msg},
		ID:      id,
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.Error("rest: encode response", "err", err)
	}
}

// codeFromError extracts the JSON-RPC error code from a dispatcher
// error if the error carries one (via the codedError interface), else
// returns -32603 Internal error. Mirrors the behaviour of the socket
// adapter's toRPCError but without bringing in the socket package.
func codeFromError(err error) int {
	type rpcCoder interface{ RPCCode() int }
	var coder rpcCoder
	if errors.As(err, &coder) {
		return coder.RPCCode()
	}
	return -32603
}
