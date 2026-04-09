# Contract: Dispatcher Interface

**Feature**: 004-socket-adapter
**Consumers**: feature 005 (application use cases), feature 006 (`cmd/wad` composition root)

The `Dispatcher` interface is the one seam between the socket adapter (feature 004) and the business logic that lives in `internal/app/` (feature 005). This document is the binding contract: the exact shape, the context semantics, the error-return conventions, and the event-source semantics. Any change to this interface is a breaking change to every primary adapter that consumes it.

## Go declaration

```go
// Package socket — primary adapter; see doc.go for scope.

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
```

## Semantic contract

### For dispatcher authors (feature 005 and beyond)

1. **`Handle` must be safe to call from multiple goroutines concurrently.** The adapter pipelines up to 32 in-flight requests per connection and processes many connections concurrently. A dispatcher that holds internal mutable state MUST guard it with a mutex or channel.

2. **`Handle` must honor `ctx.Done()`.** Long-running operations (WhatsApp network calls, SQLite queries) must be bounded by the context. A dispatcher that ignores cancellation will hold the adapter's shutdown routine at the drain deadline — the adapter will then cancel the context anyway, and the dispatcher's work will be wasted.

3. **`Handle` must return typed errors from a documented set.** The adapter translates typed errors into JSON-RPC error codes via an explicit table. An untyped error (e.g., `fmt.Errorf("oops")`) will be mapped to `-32603 Internal error` and logged at ERROR level. Feature 005's use cases MUST return errors from the sentinel set declared in `internal/domain/errors.go` so the mapping is unambiguous.

4. **`Handle` must not log `params` or `result`.** The adapter refuses to log them; the dispatcher is equally responsible. Sensitive data (message bodies, JIDs, pairing codes) must never reach the log file.

5. **`Events()` must return the same channel on every call.** The adapter calls `Events()` exactly once at startup; caching and reusing the returned reference. If a dispatcher returns a different channel on a later call, the adapter will not see the newer one.

6. **`Events()` must be closed by the dispatcher when the event source is exhausted.** Typical cause: the daemon is shutting down and the secondary adapter (whatsmeow in the production path) has received a disconnect signal. The adapter reacts to close by emitting `-32005 SubscriptionClosed` to all subscribing connections.

7. **Events must be immutable after send.** Once a dispatcher writes an `Event` to the channel, it must not mutate the struct or its `Payload` — the adapter may be marshaling it on another goroutine.

### For the adapter (feature 004)

1. **The adapter owns the `Handle` goroutine.** Each request runs on a goroutine spawned by the adapter's jrpc2 server integration. The adapter enforces the 32-in-flight-per-connection cap at the read side.

2. **The adapter owns the subscription filter.** Dispatchers emit events without knowing which connections care; the adapter's per-connection filter table (keyed by `Event.Type`) decides who gets what.

3. **The adapter owns the subscription id.** The dispatcher does not mint ids; the adapter generates a UUID per `subscribe` call and stamps it into outgoing events via the `SubscriptionId` field.

4. **The adapter owns graceful shutdown of subscribers.** When the server begins draining, the adapter sends one final error frame to each subscribing connection and closes it; the dispatcher is not involved in this cleanup.

5. **The adapter never retains `params` or `result` beyond the `Handle` call.** No caching, no logging, no side tables. The bytes are handed to jrpc2 for marshaling and then forgotten.

## Fake dispatcher for tests

Feature 004 ships a `FakeDispatcher` in `sockettest/fake_dispatcher.go` implementing the interface. Behavior:

- Method handlers are registered via `fake.On(method, fn)`; calls to unregistered methods return a Method not found error.
- A test-only `fake.PushEvent(Event)` enqueues an event onto the `Events()` channel.
- `fake.Close()` closes the `Events()` channel.
- A `fake.Calls()` method returns the ordered history of `Handle` invocations for assertion purposes.

Every test in the `sockettest` package MUST be runnable against `FakeDispatcher` — no test in feature 004 may require a real use case to exist.

## Lifecycle diagram

```
            feature 005: use cases           feature 004: socket adapter
            ────────────────────              ──────────────────────────
            Dispatcher impl                   Server
              ├─ Handle(ctx, m, p) ←──RPC────── handles incoming request
              │                                 fans out per connection
              │                                 applies concurrency cap
              │
              └─ Events() <-chan Event ──────→ reads, filters by subscription
                                                pushes to bounded mailbox
                                                writer goroutine writes frame
                                                on backpressure, closes conn
```

## Test coverage requirement

Every rule in this document (numbered 1–7 for authors, 1–5 for the adapter) MUST have at least one contract test. The fake dispatcher exercises the author rules via deliberate misuse in negative tests; the adapter rules are verified by the production socket code's behavior against the fake.
