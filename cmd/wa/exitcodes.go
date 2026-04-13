package main

import "fmt"

// exitError wraps an error with a specific exit code. main() checks for
// this type to set the process exit code without calling os.Exit inside
// command handlers (which kills test processes). Pattern from gh CLI.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func (e *exitError) ExitCode() int { return e.code }

// exitf creates an exitError with a formatted message.
func exitf(code int, format string, args ...any) *exitError {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

// exiterr creates an exitError wrapping an existing error.
func exiterr(code int, err error) *exitError {
	return &exitError{code: code, err: err}
}

// rpcCodeToExit maps a JSON-RPC error code to a CLI exit code per
// contracts/exit-codes.md.
func rpcCodeToExit(code int) int {
	switch code {
	case -32011: // NotPaired
		return 10
	case -32012: // NotAllowlisted
		return 11
	case -32013: // RateLimited
		return 12
	case -32014: // WarmupActive
		return 12
	case -32003: // WaitTimeout
		return 12
	case -32015: // InvalidJID
		return 64
	case -32016: // MessageTooLarge
		return 64
	case -32602: // InvalidParams
		return 64
	case -32601: // MethodNotFound
		return 64
	default:
		return 1
	}
}
