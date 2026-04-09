# Data Model: Application Use Cases

**Feature**: 005-app-usecases
**Date**: 2026-04-09

## Runtime entities

### AppDispatcher

The central orchestrator. Holds all port references, the safety pipeline, the event bridge, and the method table.

| Field | Type | Notes |
|---|---|---|
| `sender` | `app.MessageSender` | Outbound message + markRead |
| `events` | `app.EventStream` | Inbound pull-based events |
| `contacts` | `app.ContactDirectory` | Contact lookup |
| `groups` | `app.GroupManager` | Group listing |
| `session` | `app.SessionStore` | Session existence check for pair |
| `allowlist` | `app.Allowlist` | Policy decision (pure, no I/O) |
| `audit` | `app.AuditLog` | Append-only audit |
| `history` | `app.HistoryStore` | Historical message lookup |
| `safety` | `*SafetyPipeline` | Composed middleware |
| `bridge` | `*EventBridge` | Event bridge goroutine owner |
| `methods` | `map[string]methodHandler` | Immutable method table |
| `log` | `*slog.Logger` | Base logger |
| `ctx` | `context.Context` | Cancelled on Close |
| `cancel` | `context.CancelFunc` | |

**Invariant**: After construction, `methods` is immutable. After `Close()`, `Handle()` returns a shutdown error.

### SafetyPipeline

Composes allowlist + rate limiter + warmup into a single `Check(jid, action) error` call.

| Field | Type | Notes |
|---|---|---|
| `allowlist` | `app.Allowlist` | Injected from AppDispatcher |
| `limiter` | `*RateLimiter` | Three-bucket limiter with warmup |

**Pipeline order**: allowlist → rate limiter (which includes warmup). If allowlist denies, rate limiter is not consulted (no token consumed).

### RateLimiter

Three independent token buckets with a warmup multiplier.

| Field | Type | Notes |
|---|---|---|
| `perSecond` | `*rate.Limiter` | Default: 2 tokens/s, burst 2 |
| `perMinute` | `*rate.Limiter` | Default: 30 tokens/min (0.5/s), burst 30 |
| `perDay` | `*rate.Limiter` | Default: 1000 tokens/day (~0.012/s), burst 1000 |
| `warmup` | `float64` | 0.25, 0.50, or 1.0 |
| `sessionCreated` | `time.Time` | Used to recompute warmup |

**Warmup recalculation**: Called once at construction and optionally on a daily ticker. Uses `SetLimit`/`SetBurst` to adjust all three buckets.

| Session age | Multiplier | Effective per-second | Effective per-minute | Effective per-day |
|---|---|---|---|---|
| 0-7 days | 0.25 | 0.5/s, burst 1 | 7/min | 250/day |
| 7-14 days | 0.50 | 1.0/s, burst 1 | 15/min | 500/day |
| 14+ days | 1.00 | 2.0/s, burst 2 | 30/min | 1000/day |

### EventBridge

The goroutine that reads from `EventStream.Next()` and fans out to both `Events()` and registered `wait` waiters.

| Field | Type | Notes |
|---|---|---|
| `stream` | `app.EventStream` | The pull-based port |
| `out` | `chan AppEvent` | The push channel returned by `Events()` |
| `waiters` | `[]waiter` | Registered `wait` method blockers |
| `mu` | `sync.Mutex` | Guards `waiters` |
| `ctx` | `context.Context` | Cancelled on dispatcher shutdown |

**Waiter struct**:

| Field | Type | Notes |
|---|---|---|
| `filter` | `map[string]struct{}` | Event types to match (empty = all) |
| `ch` | `chan AppEvent` | Capacity 1; the `wait` handler blocks on this. If two events match before the caller reads, the second is dropped (caller only wants the first). |

### AppEvent

The app-layer event type. Structurally similar to `socket.Event` but owned by `internal/app/` to avoid importing the socket adapter (D2).

| Field | Type | JSON | Notes |
|---|---|---|---|
| `Type` | `string` | `"type"` | Event type name: "message", "receipt", "status", "pairing" |
| `Payload` | `any` | — | The domain event, marshaled by the composition root adapter |

### MethodHandler

```go
type methodHandler func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)
```

Populated at construction; each handler captures the port references it needs via closure.

### Typed errors

| Error | Code | When |
|---|---|---|
| `ErrNotAllowlisted` | -32012 | Allowlist.Allows returns false |
| `ErrRateLimited` | -32013 | Any rate bucket is exhausted |
| `ErrWarmupActive` | -32014 | Warmup multiplier rejects (same as rate limited but distinct code) |
| `ErrNotPaired` | -32011 | `pair` called but session already exists; or send called but no session |
| `ErrInvalidJID` | -32015 | JID parsing failed |
| `ErrMessageTooLarge` | -32016 | Message body exceeds domain limit |
| `ErrDisconnected` | -32018 | WhatsApp upstream not connected |
| `ErrWaitTimeout` | -32003 | `wait` timed out (reuses RequestTimeout code) |

All implement the `codedError` interface (`RPCCode() int`) so the socket adapter can map them.

## LOC budget

| File | Estimate |
|---|---|
| `dispatcher.go` | 120 |
| `method_send.go` | 100 |
| `method_pair.go` | 50 |
| `method_status.go` | 60 |
| `method_markread.go` | 40 |
| `method_wait.go` | 60 |
| `safety.go` | 50 |
| `ratelimiter.go` | 80 |
| `eventbridge.go` | 100 |
| `events.go` | 20 |
| `errors.go` | 50 |
| **Production subtotal** | **730** |
| `dispatcher_test.go` | 150 |
| `safety_test.go` | 100 |
| `ratelimiter_test.go` | 100 |
| `eventbridge_test.go` | 100 |
| `method_send_test.go` | 100 |
| **Test subtotal** | **550** |
| Existing file changes (ports.go, memory/adapter.go, whatsmeow/) | ~50 |
| **Total** | **~1330** |
