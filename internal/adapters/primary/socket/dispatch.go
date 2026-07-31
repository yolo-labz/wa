package socket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const invalidParamsMsg = "Invalid params: %v"

// dispatchAssigner implements jrpc2.Assigner by routing every method to the
// injected Dispatcher. This is the bridge between jrpc2 and our Dispatcher
// interface. The "subscribe" and "unsubscribe" methods are intercepted before
// reaching the Dispatcher.
type dispatchAssigner struct {
	server *Server
	conn   *Connection
}

// Assign returns a handler for the given method name. The "subscribe" and
// "unsubscribe" methods are intercepted and handled by the server itself;
// all others are routed through the Dispatcher.
func (a *dispatchAssigner) Assign(_ context.Context, method string) jrpc2.Handler {
	switch method {
	case "subscribe":
		return handler.New(a.server.makeConnFunc(a.conn, a.server.handleSubscribe))
	case "unsubscribe":
		return handler.New(a.server.makeConnFunc(a.conn, a.server.handleUnsubscribe))
	case "subscribe.pong":
		return handler.New(a.server.makeConnFunc(a.conn, a.server.handlePong))
	default:
		return handler.New(a.server.makeDispatchFunc(method))
	}
}

// Names returns nil — the set of methods is open-ended and defined by the
// Dispatcher, not by the socket adapter.
func (a *dispatchAssigner) Names() []string {
	return nil
}

// makeDispatchFunc creates a closure that dispatches a specific method to the
// Dispatcher, with panic recovery and error translation.
func (s *Server) makeDispatchFunc(method string) func(context.Context, *jrpc2.Request) (any, error) {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		// Reject new requests if shutdown is in progress.
		if s.ShutdownStarted() {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeShutdownInProgress), "%s", errCodeName[CodeShutdownInProgress])
		}

		// Extract raw params from the request.
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}

		return s.dispatchRecovered(ctx, method, req.ID(), params)
	}
}

// dispatchRecovered invokes the Dispatcher with panic recovery (SF-09).
// A recovered panic logs the stack plus a correlation ref and returns
// the same ref in the wire message, so an operator can tie a client's
// "Internal error (ref …)" report to the exact server log line without
// any internal detail (stack, panic value) crossing the trust boundary.
func (s *Server) dispatchRecovered(ctx context.Context, method, reqID string, params json.RawMessage) (result any, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			ref := panicRef()
			s.log.Error(
				"panic in dispatcher",
				"method", method,
				"ref", ref,
				"requestID", reqID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
			retErr = jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error (ref %s)", ref)
		}
	}()

	// Dispatch to the injected Dispatcher.
	raw, err := s.dispatcher.Handle(ctx, method, params)
	if err != nil {
		return nil, toRPCError(err)
	}

	// Return the raw JSON result. handler already returns
	// json.RawMessage so jrpc2 does not double-encode.
	if raw == nil {
		// nil result = JSON-RPC success with null result; a sentinel
		// error here would change the wire contract.
		return nil, nil //nolint:nilnil // intentional null-result success
	}
	return raw, nil
}

// panicRef returns a short random correlation token (8 hex chars) for
// pairing a panic log line with the client-visible error message. Falls
// back to a fixed marker if the system RNG is unavailable — correlation
// degrades but the RPC error path must not fail inside recover.
func panicRef() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "norand"
	}
	return hex.EncodeToString(b[:])
}

// connHandler is the shape of the three connection-scoped methods
// (subscribe, unsubscribe, subscribe.pong). Unlike a dispatched method they
// need the *Connection they arrived on, so they cannot go through the
// Dispatcher.
type connHandler func(context.Context, *Connection, json.RawMessage) (json.RawMessage, error)

// makeConnFunc adapts a connHandler into a jrpc2 handler func, unmarshalling
// raw params and passing the connection through.
//
// Unlike makeDispatchFunc there is no panic recovery here: these handlers
// touch only this adapter's own subscription bookkeeping, never the
// Dispatcher, so a panic is a socket-adapter bug that must surface rather
// than be folded into a correlation ref.
func (s *Server) makeConnFunc(conn *Connection, h connHandler) func(context.Context, *jrpc2.Request) (any, error) {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}
		raw, err := h(ctx, conn, params)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, nil
		}
		return raw, nil
	}
}

// serverOptions returns the jrpc2.ServerOptions for a per-connection server.
func (s *Server) serverOptions() *jrpc2.ServerOptions {
	return &jrpc2.ServerOptions{
		AllowPush:   true,
		Concurrency: s.maxInFlight,
	}
}

// codedError is an error that carries a JSON-RPC error code. Implementations
// (e.g., sockettest.RPCError) satisfy this interface so the error translation
// layer can extract the code without importing test packages.
type codedError interface {
	error
	RPCCode() int
}

// toRPCError translates a dispatcher error into a jrpc2.Error with the
// appropriate JSON-RPC error code from the error code table. The body
// is a flat translation table; extracting branches into helpers
// scatters the audit trail tying each sentinel to its wire code.
//
//nolint:gocyclo // flat sentinel-to-code translation; extraction scatters audit
func toRPCError(err error) error {
	if err == nil {
		return nil
	}

	// Already a jrpc2.Error — pass through.
	var jrpcErr *jrpc2.Error
	if errors.As(err, &jrpcErr) {
		return jrpcErr
	}

	// Map sentinel errors to their JSON-RPC codes.
	switch {
	case errors.Is(err, ErrBackpressure):
		return jrpc2.Errorf(jrpc2.Code(CodeBackpressure), "%s", errCodeName[CodeBackpressure])
	case errors.Is(err, ErrShutdown):
		return jrpc2.Errorf(jrpc2.Code(CodeShutdownInProgress), "%s", errCodeName[CodeShutdownInProgress])
	case errors.Is(err, domain.ErrMessageTooLarge):
		return jrpc2.Errorf(jrpc2.Code(CodeMediaTooLarge), "%s: %s", errCodeName[CodeMediaTooLarge], err.Error())
	case errors.Is(err, domain.ErrIdempotencyCollision):
		return jrpc2.Errorf(jrpc2.Code(CodeIdempotencyCollision), "%s: %s", errCodeName[CodeIdempotencyCollision], err.Error())
	case errors.Is(err, domain.ErrOutsideEditWindow):
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	case errors.Is(err, domain.ErrPastMuteTimestamp):
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	case errors.Is(err, domain.ErrBlocked):
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	case errors.Is(err, domain.ErrNotAdmin):
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	case errors.Is(err, domain.ErrEmptyGroupPatch):
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	case errors.Is(err, app.ErrNotAllowlisted):
		// FR-050: allowlist refusal uses -32100 policy_refused with a
		// constant message so the wire body is byte-identical for any
		// refused (jid, method) pair — no JID existence probing.
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s", errCodeName[CodePolicyRefused])
	case errors.Is(err, domain.ErrUpstreamError):
		// -32000 shared slot with PeerCredRejected / ProtocolMismatch per
		// the JSON-RPC v2 contract; message field disambiguates.
		return jrpc2.Errorf(jrpc2.Code(CodePeerCredRejected), "upstream_error: %s", err.Error())
	case errors.Is(err, domain.ErrMediaUnsupported):
		return jrpc2.Errorf(jrpc2.Code(CodeUnsupportedMessageType), "%s: %s", errCodeName[CodeUnsupportedMessageType], err.Error())
	case errors.Is(err, domain.ErrMediaNotCached):
		return jrpc2.Errorf(jrpc2.Code(CodeMediaNotCached), "%s: %s", errCodeName[CodeMediaNotCached], err.Error())
	case errors.Is(err, domain.ErrBroadcastForbidden):
		// CLAUDE.md §Safety hard-refuses broadcast list traffic. Map to
		// -32100 PolicyRefused so the wire shape matches every other
		// safety refusal — callers branch on the code, the message
		// gives a constant explanation. Spec 108.
		return jrpc2.Errorf(jrpc2.Code(CodePolicyRefused), "%s: %s", errCodeName[CodePolicyRefused], err.Error())
	}

	// Check for errors carrying a numeric code (e.g., sockettest.RPCError).
	var coded codedError
	if errors.As(err, &coded) {
		return jrpc2.Errorf(jrpc2.Code(coded.RPCCode()), "%s", coded.Error()) //nolint:gosec // G115: RPCCode() returns a small controlled error-code constant
	}

	// Fallback: internal error.
	return jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
}
