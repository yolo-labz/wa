package main

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
