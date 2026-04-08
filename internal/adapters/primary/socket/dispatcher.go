package socket

import (
	"context"
	"encoding/json"
)

// Dispatcher is the seam between the socket transport and the business use cases.
// Implementations live outside this package (feature 005 will provide the first
// production implementation in internal/app/, feature 004 ships a FakeDispatcher
// in the sockettest package for contract testing).
//
// A Dispatcher is stateless from the socket adapter's perspective. It has two
// responsibilities: (1) handle an incoming request by method name, and (2) expose
// an event source that the socket adapter forwards to subscribing connections.
type Dispatcher interface {
	// Handle dispatches a single JSON-RPC request to its implementation.
	// method is the case-sensitive method name extracted from the request envelope.
	// params is the raw JSON bytes of the params field (may be nil if the request
	// omitted params). The returned bytes are the raw JSON result the adapter will
	// marshal into a success response. A typed error return is translated into a
	// JSON-RPC error response by the adapter via the error code table (see
	// contracts/wire-protocol.md).
	//
	// The context is a child of the per-connection context, itself a child of the
	// server's root context. Cancellation semantics:
	//   - the caller (socket adapter) cancels this context if the client disconnects
	//   - the caller cancels this context if graceful shutdown begins before the
	//     request completes and the shutdown drain deadline elapses
	//   - implementations MUST honor ctx.Done() and return promptly on cancellation
	//
	// Implementations MUST NOT:
	//   - panic for reasons other than programmer error (panics are recovered and
	//     mapped to -32603 Internal error by the adapter)
	//   - log the contents of params or the returned result at any level
	//   - retain references to params beyond the return of this call
	//   - spawn goroutines that outlive this call unless they are tied to the
	//     Events() channel below
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)

	// Events returns a channel from which the socket adapter reads events to
	// forward to subscribing connections. The channel is owned by the dispatcher
	// and closed by the dispatcher when the event source is exhausted (normally
	// at daemon shutdown). The adapter never closes this channel.
	//
	// The returned channel MUST be the same instance across calls — the adapter
	// calls Events() once at server startup and retains the reference.
	//
	// Events are fan-out-filtered by the adapter per subscription. A dispatcher
	// that emits an event does not know which connections will receive it; the
	// adapter owns the per-connection subscription filter (by event type name).
	//
	// If the channel closes while connections hold active subscriptions, the
	// adapter sends one final -32005 SubscriptionClosed error to each subscribing
	// connection and releases the subscriptions.
	Events() <-chan Event
}

// Event is what the dispatcher pushes through its Events() channel. The adapter
// reads Event.Type to match against per-connection subscription filters, then
// marshals the whole struct into the params field of a server notification per
// the wire protocol.
type Event struct {
	// Type is the event type name that subscription filters key on.
	// Examples for feature 005: "message", "receipt", "pairing", "status".
	Type string `json:"type"`

	// SubscriptionId is filled in by the adapter before the event goes on the
	// wire; dispatchers leave it empty.
	SubscriptionId string `json:"subscriptionId,omitempty"`

	// Payload is the event-specific fields, inlined into params at marshal time.
	// Dispatchers provide this as any marshal-able Go value; the adapter will
	// marshal it and merge the fields into the notification params object.
	Payload any `json:"-"`
}
