package sockettest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// HandlerFunc is the signature for a method handler registered on a
// FakeDispatcher. It receives the context, method name, and raw JSON params
// and returns a raw JSON result or an error.
type HandlerFunc func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)

// Call records a single Handle invocation for assertion purposes.
type Call struct {
	Method string
	Params json.RawMessage
}

// FakeDispatcher implements socket.Dispatcher for contract tests. Method
// handlers are registered via On; unregistered methods return a method-not-found
// error. Events can be pushed via PushEvent and the channel closed via Close.
type FakeDispatcher struct {
	handlers map[string]HandlerFunc
	events   chan socket.Event
	calls    []Call
	mu       sync.Mutex
	closed   bool
}

// compile-time interface check
var _ socket.Dispatcher = (*FakeDispatcher)(nil)

// NewFakeDispatcher creates a FakeDispatcher with an events channel of
// capacity 256.
func NewFakeDispatcher() *FakeDispatcher {
	return &FakeDispatcher{
		handlers: make(map[string]HandlerFunc),
		events:   make(chan socket.Event, 256),
	}
}

// On registers a handler for the given method name. It replaces any
// previously registered handler for that method.
func (f *FakeDispatcher) On(method string, fn HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = fn
}

// Handle dispatches a request to the registered handler, recording the call.
// If no handler is registered for the method, it returns a JSON-RPC
// method-not-found error using the error code from errcodes.go.
func (f *FakeDispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: method, Params: params})
	fn, ok := f.handlers[method]
	f.mu.Unlock()

	if !ok {
		return nil, &RPCError{
			Code:    int(socket.CodeMethodNotFound),
			Message: "Method not found",
			Data:    method,
		}
	}
	return fn(ctx, method, params)
}

// Events returns the read-only events channel. The same channel instance is
// returned on every call.
func (f *FakeDispatcher) Events() <-chan socket.Event {
	return f.events
}

// PushEvent sends an event onto the events channel. It blocks if the channel
// buffer is full.
func (f *FakeDispatcher) PushEvent(e socket.Event) {
	f.events <- e
}

// Close closes the events channel. It is idempotent — calling Close multiple
// times is safe. After Close, PushEvent will panic.
func (f *FakeDispatcher) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
}

// Calls returns a copy of the ordered call history.
func (f *FakeDispatcher) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// RPCError is a simple typed error used by FakeDispatcher to signal JSON-RPC
// error conditions (e.g., method not found). The socket adapter's error
// translation layer will map these to wire-level JSON-RPC error responses.
type RPCError struct {
	Code    int
	Message string
	Data    any
}

func (e *RPCError) Error() string {
	return e.Message
}

// RPCCode returns the JSON-RPC error code. This satisfies the codedError
// interface used by the socket adapter's error translation layer.
func (e *RPCError) RPCCode() int {
	return e.Code
}
