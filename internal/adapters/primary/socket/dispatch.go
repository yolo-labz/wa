package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
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
		return handler.New(a.server.makeSubscribeFunc(a.conn))
	case "unsubscribe":
		return handler.New(a.server.makeUnsubscribeFunc(a.conn))
	case "subscribe.pong":
		return handler.New(a.server.makePongFunc(a.conn))
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
	return func(ctx context.Context, req *jrpc2.Request) (result any, retErr error) {
		// Reject new requests if shutdown is in progress.
		if s.ShutdownStarted() {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeShutdownInProgress), "%s", errCodeName[CodeShutdownInProgress])
		}

		// Recover from panics in the dispatcher.
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("panic in dispatcher",
					"method", method,
					"panic", fmt.Sprintf("%v", r),
				)
				retErr = jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
			}
		}()

		// Extract raw params from the request.
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}

		// Dispatch to the injected Dispatcher.
		raw, err := s.dispatcher.Handle(ctx, method, params)
		if err != nil {
			return nil, toRPCError(err)
		}

		// Return the raw JSON result. Wrap in json.RawMessage so jrpc2
		// does not double-encode it.
		if raw == nil {
			return nil, nil
		}
		return json.RawMessage(raw), nil
	}
}

// makeSubscribeFunc creates a closure that handles the "subscribe" method,
// delegating to Server.handleSubscribe with the connection context.
func (s *Server) makeSubscribeFunc(conn *Connection) func(context.Context, *jrpc2.Request) (any, error) {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}
		raw, err := s.handleSubscribe(ctx, conn, params)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, nil
		}
		return raw, nil
	}
}

// makeUnsubscribeFunc creates a closure that handles the "unsubscribe" method,
// delegating to Server.handleUnsubscribe with the connection context.
func (s *Server) makeUnsubscribeFunc(conn *Connection) func(context.Context, *jrpc2.Request) (any, error) {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}
		raw, err := s.handleUnsubscribe(ctx, conn, params)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, nil
		}
		return raw, nil
	}
}

// makePongFunc creates a closure that handles the "subscribe.pong" method,
// delegating to Server.handlePong with the connection context.
func (s *Server) makePongFunc(conn *Connection) func(context.Context, *jrpc2.Request) (any, error) {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var params json.RawMessage
		if req.HasParams() {
			if err := req.UnmarshalParams(&params); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), invalidParamsMsg, err)
			}
		}
		raw, err := s.handlePong(ctx, conn, params)
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
// appropriate JSON-RPC error code from the error code table.
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
	}

	// Check for errors carrying a numeric code (e.g., sockettest.RPCError).
	var coded codedError
	if errors.As(err, &coded) {
		return jrpc2.Errorf(jrpc2.Code(coded.RPCCode()), "%s", coded.Error()) //nolint:gosec // bounded by maxInFlight (32)
	}

	// Fallback: internal error.
	return jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
}
