// Package socket implements the JSON-RPC 2.0 primary adapter over a unix
// domain socket. It is the transport layer between wa CLI clients and the
// daemon's use cases. No business logic lives here.
package socket

// Dependency pin: imported in production code from Phase 2 onward.
// This blank import ensures go mod tidy retains the module in go.mod
// until real imports land.
import _ "github.com/creachadair/jrpc2"
