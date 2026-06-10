package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"

	"github.com/yolo-labz/wa/v2/internal/domain"
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
//
// events is the optional spec 110b extension surface; when nil,
// GET /v1/events returns 503 with a JSON-RPC error envelope.
type Server struct {
	dispatcher Dispatcher
	auth       Authenticator
	events     EventStream
	media      MediaResolver
	mediaUp    MediaWriter
	mcp        MCPProvider
	scoped     bool
	log        *slog.Logger
	srv        *http.Server
	listener   net.Listener
}

// MCPProvider returns the MCP HTTP handler for a granted token scope
// (feature 111 M2). The provider implements scope-filtered tool
// registration by returning a DIFFERENT pre-built handler per scope
// class — read-scope tokens reach a server with only read tools
// registered, so unauthorized tools are undiscoverable rather than
// merely refused. A nil return fails closed (403).
type MCPProvider func(scope Scope) http.Handler

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

// WithEventStream wires the spec 110b SSE extension. When unset, the
// server's GET /v1/events handler returns 503 with a JSON-RPC error
// envelope so clients can parse failure uniformly.
func WithEventStream(events EventStream) ServerOption {
	return func(s *Server) {
		s.events = events
	}
}

// WithMediaStore wires the issue #169 content-addressed media fetch
// route. When unset, GET /media/{sha256} returns 503 with a JSON-RPC
// error envelope so a remote client gets an actionable failure rather
// than a bare 404.
func WithMediaStore(m MediaResolver) ServerOption {
	return func(s *Server) {
		if m != nil {
			s.media = m
		}
	}
}

// WithMediaUploader wires the spec 198 content-addressed upload route
// POST /media/upload. When unset, the route returns 503 with a JSON-RPC
// error envelope so a remote client gets an actionable failure rather
// than a bare 404.
func WithMediaUploader(m MediaWriter) ServerOption {
	return func(s *Server) {
		if m != nil {
			s.mediaUp = m
		}
	}
}

// WithMCP mounts the Model Context Protocol endpoint at /mcp (feature
// 111 M2, Streamable HTTP). The route is registered only when a
// provider is supplied, so daemons without MCP wiring expose nothing.
func WithMCP(p MCPProvider) ServerOption {
	return func(s *Server) {
		if p != nil {
			s.mcp = p
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
	// Spec 110d: detect scoped (multi-token) auth so handleRPC can
	// enforce per-method MethodScope. Type assertion is the cleanest
	// signal — scopedAuth is unexported, so only NewScopedAuth can
	// produce one.
	if _, ok := auth.(*scopedAuth); ok {
		s.scoped = true
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/rpc", s.handleRPC)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /media/{sha256}", s.handleMediaFetch)
	mux.HandleFunc("POST /media/upload", s.handleMediaUpload)
	// Feature 111 / roadmap 0.2 — agent-readable surface. Both routes
	// are deliberately unauthenticated (like /v1/version): they carry
	// no secrets and exist so unauthenticated agents can discover how
	// to integrate and what error codes mean.
	mux.HandleFunc("GET /llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(agentdocs.LLMsTxt)
	})
	mux.HandleFunc("GET /v1/errors", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentdocs.ErrorsJSON)
	})
	if s.mcp != nil {
		mux.Handle("/mcp", http.HandlerFunc(s.handleMCP))
	}

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
		// Bearer-token JSON-RPC has no business shipping more than a
		// handful of headers. Cap at 32 KiB to make slowloris-style
		// header floods cheap to refuse. Codex review PR 110a §LOW.
		MaxHeaderBytes: 32 << 10,
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

	// Codex-review fix: panic recovery so a dispatcher-side panic
	// does not crash the HTTP server (default behaviour is
	// connection-tier recover; a controlled JSON-RPC envelope is
	// strictly better). Mirrors the socket adapter at
	// internal/adapters/primary/socket/dispatch.go:59.
	defer func() {
		if rec := recover(); rec != nil {
			// Capture the stack at the panic site. Without it the
			// recover log is just the panic value (e.g. "nil pointer
			// dereference") with no file:line — undiagnosable, which is
			// exactly what happened on wa-burocracy 2026-06-01. The stack
			// names the offending method + nil path for the next occurrence.
			s.log.Error("rest: panic in dispatcher",
				"panic", fmt.Sprintf("%v", rec),
				"stack", string(debug.Stack()))
			s.writeError(w, nil, http.StatusInternalServerError, -32603, "internal error")
		}
	}()

	if err := s.auth.Verify(r); err != nil {
		s.writeError(w, nil, http.StatusUnauthorized, -32099, "unauthorized")
		return
	}

	// Tolerate Content-Type with parameters (e.g. `application/json;
	// charset=utf-8`) which clients legitimately send. Codex review
	// PR 110a §LOW.
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
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

	// Spec 110d: per-method scope enforcement. The scoped auth path
	// stashes the token's scope on the request context; we read it
	// here and reject methods the scope cannot reach. Env-var auth
	// (s.scoped == false) skips this — single-token deploys treat
	// every authenticated call as admin.
	if s.scoped {
		granted := scopeFromContext(r.Context(), true)
		if !AllowedScope(req.Method, granted.MethodScope()) {
			s.writeError(w, req.ID, http.StatusForbidden, -32099, "scope insufficient for method")
			s.log.Info("rest: scope refusal", "method", req.Method, "granted", string(granted))
			return
		}
	}
	// Codex review §MEDIUM: require an id field. JSON-RPC 2.0 allows
	// notifications (no id), but our daemon's mutating methods are
	// not designed to be called as notifications — accepting them
	// silently would let a malicious client trigger send/allow/etc
	// without the round-trip the audit log expects.
	if len(req.ID) == 0 {
		s.writeError(w, nil, http.StatusBadRequest, -32600, `"id" field is required (notifications not supported)`)
		return
	}

	result, dispatchErr := s.dispatcher.Handle(r.Context(), req.Method, req.Params)
	if dispatchErr != nil {
		// Codex review §MEDIUM: typed dispatcher errors carry an
		// RPCCode that we propagate, but the wire MESSAGE is a
		// constant "rpc error" rather than the raw dispatchErr.Error()
		// to avoid leaking internal paths or upstream details. The
		// socket adapter does the same at dispatch.go:236 (constant
		// "Internal error" for the fallback). Untyped errors collapse
		// to -32603 Internal error.
		code := codeFromError(dispatchErr)
		msg := "internal error"
		if code != -32603 {
			msg = "rpc error"
		}
		s.writeError(w, req.ID, http.StatusOK, code, msg)
		// Log the underlying error for operator-side debugging.
		s.log.Error("rest: dispatcher error", "method", req.Method, "code", code, "err", dispatchErr.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	})
}

// handleMCP authenticates the request, resolves the granted token
// scope, and delegates to the scope-class MCP handler (feature 111
// M2). Auth mirrors handleEvents: bearer token via s.auth.Verify;
// failures answer with the JSON-RPC error envelope REST clients
// already parse. Scope resolution mirrors handleRPC: env-token auth
// implies admin, scoped store reads the context value, anything
// unresolved fails closed.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Verify(r); err != nil {
		w.Header().Set("Content-Type", "application/json")
		s.writeError(w, nil, http.StatusUnauthorized, -32099, "unauthorized")
		return
	}
	scope := scopeFromContext(r.Context(), s.scoped)
	h := s.mcp(scope)
	if h == nil {
		w.Header().Set("Content-Type", "application/json")
		s.writeError(w, nil, http.StatusForbidden, -32099, "scope not permitted on /mcp")
		return
	}
	h.ServeHTTP(w, r)
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

// handleMediaFetch implements GET /media/{sha256} (issue #169). It
// streams the content-addressed bytes already cached daemon-side back
// to a `--remote` CLI host, which cannot reach the daemon-side path
// that media.resolve/media.download return.
//
// Same bearer-token gate as POST /v1/rpc. Because the key is the
// content sha256, the response is immutable: it carries a strong ETag
// and Cache-Control: immutable, and delegates Range / If-None-Match /
// If-Range handling to http.ServeContent. Error responses reuse the
// JSON-RPC envelope (Content-Type application/json) so a client can
// parse failures the same way it parses an RPC error:
//
//	401 unauthorized            missing/invalid token
//	400 -32602 invalid params   sha256 not 64 hex chars
//	404 -32099                  sha256 not in the on-disk cache
//	503 -32601                  media route not enabled on this daemon
func (s *Server) handleMediaFetch(w http.ResponseWriter, r *http.Request) {
	jsonErr := func(status, code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		s.writeError(w, nil, status, code, msg)
	}

	if err := s.auth.Verify(r); err != nil {
		jsonErr(http.StatusUnauthorized, -32099, "unauthorized")
		return
	}
	if s.media == nil {
		jsonErr(http.StatusServiceUnavailable, -32601, "media fetch route not enabled on this daemon")
		return
	}

	shaHex := r.PathValue("sha256")
	sha, err := parseHexSHA256(shaHex)
	if err != nil {
		jsonErr(http.StatusBadRequest, -32602, "sha256 must be 64 lowercase hex characters")
		return
	}

	obj, err := s.media.Resolve(r.Context(), sha)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(http.StatusNotFound, -32099, "media not found")
			return
		}
		s.log.Error("rest: media resolve", "sha", shaHex, "err", err.Error())
		jsonErr(http.StatusInternalServerError, -32603, "internal error")
		return
	}

	// obj.Path comes from the media store keyed by a 64-hex sha, never from client input (G304-safe).
	f, err := os.Open(obj.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(http.StatusNotFound, -32099, "media not found")
			return
		}
		s.log.Error("rest: media open", "path", obj.Path, "err", err.Error())
		jsonErr(http.StatusInternalServerError, -32603, "internal error")
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s.log.Error("rest: media stat", "path", obj.Path, "err", fmt.Sprintf("%v", err))
		jsonErr(http.StatusInternalServerError, -32603, "internal error")
		return
	}

	w.Header().Set("Content-Type", obj.MimeDetected)
	w.Header().Set("ETag", `"`+shaHex+`"`)
	// Content-addressed ⇒ the bytes for a given sha never change.
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ServeContent sets Content-Length and Accept-Ranges, honours Range /
	// If-Range / If-None-Match against the ETag above, and never sniffs
	// because Content-Type is already set.
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// handleMediaUpload implements POST /media/upload (spec 198). It accepts a
// raw octet-stream body from a `--remote` CLI host, content-addresses it, and
// persists it to the daemon's media store so a subsequent `sendMedia --sha256`
// (or `media fetch`) can reference the bytes the daemon could not otherwise
// see on its own filesystem.
//
// SECURITY contract:
//   - Same bearer-token gate as POST /v1/rpc. Under scoped (multi-token) auth
//     this is a send-class capability: the token must hold the `send` scope
//     (media.upload → ScopeSend in MethodScopes), else 403. The env-var
//     single-token path grants admin implicitly, same as every other route.
//   - The body is read through its OWN http.MaxBytesReader at exactly
//     domain.MaxMediaBytes (16 MiB) — it deliberately does NOT route through
//     handleRPC, so it is not subject to the 1 MiB JSON-RPC frame cap. An
//     oversize body is refused with 413 before the full payload is buffered.
//   - The store is content-addressed: the on-disk path is derived from the
//     sha256 of the bytes, never from any client-supplied string, so no path
//     traversal is reachable through this route.
//
// Error responses reuse the JSON-RPC envelope (Content-Type application/json)
// so a client parses failures the same way it parses an RPC error:
//
//	401 unauthorized            missing/invalid token
//	403 -32099                  token scope cannot reach media.upload
//	405 -32601                  body read error other than oversize
//	413 -32004                  body exceeds 16 MiB
//	503 -32601                  upload route not enabled on this daemon
func (s *Server) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	jsonErr := func(status, code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		s.writeError(w, nil, status, code, msg)
	}

	if err := s.auth.Verify(r); err != nil {
		jsonErr(http.StatusUnauthorized, -32099, "unauthorized")
		return
	}
	// Scope gate (spec 110d): mirror handleRPC. media.upload is a send-class
	// capability, so a read-only token is refused with 403. The env-var path
	// (s.scoped == false) treats every authenticated call as admin.
	if s.scoped {
		granted := scopeFromContext(r.Context(), true)
		if !AllowedScope(mediaUploadMethod, granted.MethodScope()) {
			jsonErr(http.StatusForbidden, -32099, "scope insufficient for media.upload")
			s.log.Info("rest: scope refusal", "method", mediaUploadMethod, "granted", string(granted))
			return
		}
	}
	if s.mediaUp == nil {
		jsonErr(http.StatusServiceUnavailable, -32601, "media upload route not enabled on this daemon")
		return
	}

	// Own body cap at the 16 MiB domain ceiling — independent of the 1 MiB
	// JSON-RPC frame cap on handleRPC. MaxBytesReader makes an oversize body
	// fail on Read (caught below) rather than buffering it.
	body := http.MaxBytesReader(w, r.Body, domain.MaxMediaBytes)
	defer func() { _ = body.Close() }()
	payload, err := io.ReadAll(body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			jsonErr(http.StatusRequestEntityTooLarge, -32004, "media upload exceeds 16 MiB")
			return
		}
		jsonErr(http.StatusBadRequest, -32700, "read body: "+err.Error())
		return
	}
	if len(payload) == 0 {
		jsonErr(http.StatusBadRequest, -32602, "media upload body is empty")
		return
	}

	sum := sha256.Sum256(payload)
	// DetectContentType is authoritative (it sniffs the magic bytes, ignoring
	// any client-claimed type), matching how the store records MimeDetected on
	// fetched media. ext is derived from the sniffed type so MediaRef.Validate
	// (which requires a bare, non-empty ext) is satisfied; "bin" is the
	// long-tail fallback.
	detected := http.DetectContentType(payload)
	ref := domain.MediaRef{
		SHA256: sum,
		Mime:   detected,
		Size:   int64(len(payload)),
		Ext:    extForMime(detected),
	}

	obj, err := s.mediaUp.Write(r.Context(), ref, payload, detected, 0)
	if err != nil {
		s.log.Error("rest: media upload write", "sha", ref.HexSHA256(), "err", err.Error())
		jsonErr(http.StatusInternalServerError, -32603, "internal error")
		return
	}

	s.writeJSON(w, http.StatusOK, struct {
		Schema string `json:"schema"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{
		Schema: "wa.media.upload/v1",
		SHA256: obj.Ref.HexSHA256(),
		Size:   obj.Ref.Size,
	})
}

// mediaUploadMethod is the synthetic MethodScopes key for the upload route.
// It is NOT a dispatcher method — it exists only so the scope gate above can
// reuse the same AllowedScope table the JSON-RPC path uses. Spec 198.
const mediaUploadMethod = "media.upload"

// extForMime maps a sniffed MIME type to a bare extension token (no leading
// dot, no separators) suitable for domain.MediaRef.Ext. mime.ExtensionsByType
// returns dotted extensions ("\.jpg"); we strip the dot and reject any value
// that still carries a separator. The "bin" fallback covers
// application/octet-stream and any type the stdlib table does not know.
func extForMime(mt string) string {
	exts, err := mime.ExtensionsByType(mt)
	if err == nil {
		for _, e := range exts {
			bare := strings.TrimPrefix(e, ".")
			if bare != "" && !strings.ContainsAny(bare, "/\\.") {
				return bare
			}
		}
	}
	return "bin"
}

// parseHexSHA256 decodes a 64-char lowercase hex sha256 into [32]byte.
// Rejects any other length or non-hex input.
func parseHexSHA256(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, errors.New("rest: sha256 must be 64 hex characters")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, errors.New("rest: sha256 is not valid hex")
	}
	copy(out[:], raw)
	return out, nil
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
