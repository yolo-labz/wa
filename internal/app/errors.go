package app

import "errors"

// codedError is the interface typed errors implement so the socket adapter
// can map them to JSON-RPC error codes (FR-039).
type codedError interface {
	RPCCode() int
}

// rpcErr is the concrete typed error. Each sentinel wraps a base error
// so errors.Is works against the sentinel AND the base.
type rpcErr struct {
	code int
	msg  string
	base error
}

func (e *rpcErr) Error() string { return e.msg }
func (e *rpcErr) RPCCode() int  { return e.code }
func (e *rpcErr) Unwrap() error { return e.base }

// newRPCErr creates a typed rpc error with a distinct base sentinel.
func newRPCErr(code int, msg string) *rpcErr {
	base := errors.New(msg)
	return &rpcErr{code: code, msg: msg, base: base}
}

// Typed errors — codes from data-model.md §Typed errors and spec FR-039.
var (
	ErrNotPaired      = newRPCErr(-32011, "not paired")
	ErrNotAllowlisted = newRPCErr(-32012, "not allowlisted")
	ErrRateLimited    = newRPCErr(-32013, "rate limited")
	ErrWarmupActive   = newRPCErr(-32014, "warmup active")
	ErrInvalidJID     = newRPCErr(-32015, "invalid JID")
	ErrMessageTooLarge = newRPCErr(-32016, "message too large")
	ErrDisconnected   = newRPCErr(-32018, "disconnected")
	ErrWaitTimeout    = newRPCErr(-32003, "wait timeout")
	ErrMethodNotFound = newRPCErr(-32601, "method not found")
)

// IsCodedError reports whether err implements the codedError interface
// and, if so, returns the RPC code. This is the single callsite for the
// socket adapter's error-to-JSON-RPC mapping.
func IsCodedError(err error) (int, bool) {
	var ce codedError
	if errors.As(err, &ce) {
		return ce.RPCCode(), true
	}
	return 0, false
}
