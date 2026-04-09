# Contract: AppDispatcher Implementation

**Feature**: 005-app-usecases
**Consumers**: feature 006 (`cmd/wad` composition root)

## Method table

The `AppDispatcher` routes JSON-RPC method names to handler functions. The table is populated at construction and is immutable thereafter.

| Method | Handler | Safety pipeline? | Audit? | Port(s) consumed |
|---|---|---|---|---|
| `send` | `handleSend` | Yes (allowlist + rate + warmup) | Yes | MessageSender |
| `sendMedia` | `handleSendMedia` | Yes | Yes | MessageSender |
| `react` | `handleReact` | Yes | Yes | MessageSender |
| `markRead` | `handleMarkRead` | Yes | Yes | MessageSender.MarkRead |
| `pair` | `handlePair` | No | No | SessionStore (existence check) + adapter pairing |
| `status` | `handleStatus` | No | No | adapter connection state |
| `groups` | `handleGroups` | No | No | GroupManager |
| `wait` | `handleWait` | No | No | EventBridge (fan-out waiter) |

Methods not in this table return a method-not-found error.

## Handle method contract

```go
func (d *AppDispatcher) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
```

1. Look up `method` in the method table. If absent, return `ErrMethodNotFound`.
2. Call the handler function with `ctx` and `params`.
3. The handler either returns `(json.RawMessage, nil)` on success or `(nil, typedError)` on failure.
4. The caller (socket adapter, via composition-root adapter) translates the typed error to a JSON-RPC error frame.

## Safety pipeline execution order

For gated methods (`send`, `sendMedia`, `react`, `markRead`):

```
1. Parse params → extract JID + action
2. Allowlist.Allows(jid, action) → false? return ErrNotAllowlisted
3. RateLimiter.Allow() → false? return ErrRateLimited (or ErrWarmupActive)
4. Call port method (MessageSender.Send or MarkRead)
5. AuditLog.Record(outcome)
   - success: decision="ok", detail=messageId
   - port error: decision="error", detail=err.Error()
```

If step 2 fails, steps 3-5 are skipped.
If step 3 fails, steps 4-5 are skipped (but audit IS recorded for denials).

Correction to the above: audit is recorded for DENIALS too (steps 2 and 3), so:

```
1. Parse params → extract JID + action
2. Allowlist.Allows(jid, action)
   → false? audit(denied:allowlist) + return ErrNotAllowlisted
3. RateLimiter.Allow()
   → false? audit(denied:rate or denied:warmup) + return error
4. Call port method
5. audit(ok or error)
```

## Events method contract

```go
func (d *AppDispatcher) Events() <-chan AppEvent
```

Returns the bridge's output channel. Called once by the composition-root adapter at startup. The channel is closed when `Close()` is called on the dispatcher.

## Close method contract

```go
func (d *AppDispatcher) Close() error
```

1. Cancel the dispatcher's context (stops the event bridge goroutine).
2. Wait for the bridge goroutine to exit.
3. Close the `Events()` channel.
4. Return nil.

After `Close()`, `Handle()` returns `ErrShutdown` for all methods.

## Composition root adapter (feature 006)

The composition root in `cmd/wad` will contain:

```go
type dispatcherAdapter struct {
    app    *app.AppDispatcher
    events chan socket.Event
}

func (a *dispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
    return a.app.Handle(ctx, method, params)
}

func (a *dispatcherAdapter) Events() <-chan socket.Event {
    return a.events
}
```

A goroutine reads from `a.app.Events()` (returns `<-chan app.AppEvent`) and converts each `app.AppEvent` to a `socket.Event`, pushing onto `a.events`. This is ~20 lines of glue code in feature 006.
