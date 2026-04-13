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
	ErrNotPaired       = newRPCErr(-32011, "not paired")
	ErrNotAllowlisted  = newRPCErr(-32012, "not allowlisted")
	ErrRateLimited     = newRPCErr(-32013, "rate limited")
	ErrWarmupActive    = newRPCErr(-32014, "warmup active")
	ErrInvalidJID      = newRPCErr(-32015, "invalid JID")
	ErrMessageTooLarge = newRPCErr(-32016, "message too large")
	ErrDisconnected    = newRPCErr(-32018, "disconnected")
	ErrWaitTimeout     = newRPCErr(-32003, "wait timeout")
	ErrMethodNotFound  = newRPCErr(-32601, "method not found")
	ErrInvalidParams   = newRPCErr(-32602, "invalid params")
)

// IsCodedError reports whether err implements the codedError interface
// and, if so, returns the RPC code. This is the single callsite for the
// socket adapter's error-to-JSON-RPC mapping.
//
// Note: codedError is a capability interface (RPCCode() only), not an
// error interface, so errors.AsType[codedError] is not applicable —
// errors.AsType requires E to satisfy error. errors.As is correct here.
func IsCodedError(err error) (int, bool) {
	var ce codedError
	if errors.As(err, &ce) {
		return ce.RPCCode(), true
	}
	return 0, false
}

// Error aggregation note (FR-017): use errors.Join for combining
// independent errors (cleanup, validation, parallel fan-out). Single-%w
// via fmt.Errorf("context: %w", err) is for adding call-site context.
// GOTCHA: errors.Unwrap() returns nil for errors.Join results — always
// use errors.Is / errors.As (which traverse the tree correctly).
// See: golang/go#57358, Ian Lewis TIL (March 2025).
