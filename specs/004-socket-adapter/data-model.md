# Data Model: Socket Primary Adapter

**Feature**: 004-socket-adapter
**Date**: 2026-04-08

The socket adapter is a transport layer, not a persistence layer. It owns no database and no on-disk records. "Data model" in this context means: the in-memory entities that make up the server's runtime state, the wire-level envelopes that flow over the socket, and the error-code table that clients are contractually entitled to depend on.

Every entity below is declared where it is consumed — no DTOs, no service-layer indirection, per CLAUDE.md anti-pattern #6 ("No factories, DTOs, mappers, or usecase/interactor/presenter trinity").

## Runtime entities

### Server

The long-lived object that owns the listener, the lock, the base logger, the dispatcher reference, and the shutdown controller.

| Field | Type | Notes |
|---|---|---|
| `path` | `string` | Absolute filesystem path, resolved at construction via `path_linux.go`/`path_darwin.go` |
| `listener` | `net.Listener` | Nil until `Run()` starts; closed during shutdown |
| `lockUnlock` | `func()` | Returned by `lockedfile.Mutex.Lock`; called on shutdown to release the single-instance lock |
| `dispatcher` | `Dispatcher` | Pluggable seam; see [contracts/dispatcher.md](contracts/dispatcher.md) |
| `log` | `*slog.Logger` | Base logger; per-connection loggers derive from this with `.With("conn", id)` |
| `ctx` | `context.Context` | Root context; cancelled on `Shutdown()` |
| `cancel` | `context.CancelFunc` | Captured when `ctx` is created |
| `wg` | `sync.WaitGroup` | Tracks per-connection goroutines |
| `connCounter` | `atomic.Uint64` | Source of monotonic connection ids |
| `shutdownDeadline` | `time.Duration` | Default 5s; configurable via `ServerOption` |
| `maxConcurrentConns` | `int` | Default 16; soft cap enforced by logging + metrics, hard cap by OS fd limits |
| `maxInFlightPerConn` | `int` | Default 32; hard cap enforced at read side |

**Invariants**:
- `listener` is non-nil ⇒ `lockUnlock` is non-nil (lock is acquired before listener is created)
- `path` is absolute and its parent directory exists with mode `0700` before `listener` is created
- `ctx` is a descendant of the caller's context passed to `Run(ctx)`
- After `Shutdown()` returns, the socket file at `path` and the `.lock` sibling do not exist on disk

**State transitions** (monotonic):

```
new → locked → listening → draining → closed
```

- `new` — constructor returned but `Run()` not yet called
- `locked` — `lockedfile.Mutex` acquired; listener not yet created
- `listening` — `net.Listen` succeeded; `Accept` loop running
- `draining` — `ctx.Done()` fired; listener closed; in-flight connections finishing
- `closed` — `wg.Wait` returned; socket unlinked; lock released

Transitions are one-way. A `Server` cannot be restarted; construct a fresh one.

---

### Connection

Per-connection state. One `*Connection` per accepted `net.Conn`. Lifetime: from `listener.Accept()` return to the per-connection goroutine's `return`.

| Field | Type | Notes |
|---|---|---|
| `id` | `uint64` | Monotonic, assigned by `Server.connCounter.Add(1)` |
| `peerUID` | `uint32` | Set by the peer-cred check; zero before the check |
| `raw` | `*net.UnixConn` | The accepted socket connection |
| `rpcServer` | `*jrpc2.Server` | Wraps `raw` with `channel.Line` framing |
| `log` | `*slog.Logger` | Derived from `Server.log` with `conn`, `peer_uid` attrs |
| `ctx` | `context.Context` | Derived from `Server.ctx`; cancelled when connection closes |
| `cancel` | `context.CancelFunc` | |
| `subscriptions` | `map[string]*Subscription` | Keyed by subscription id; guarded by `mu` |
| `out` | `chan []byte` | Bounded outbound mailbox for push notifications; capacity 1024 |
| `inFlight` | `atomic.Int32` | Current in-flight request count; enforces `Server.maxInFlightPerConn` |
| `mu` | `sync.Mutex` | Guards `subscriptions` map |
| `createdAt` | `time.Time` | Wall clock when accepted; used only in logs |

**Invariants**:
- `peerUID` equals `os.Geteuid()` for the lifetime of the connection; if it doesn't match at accept time, the connection is closed before any bytes are read
- `inFlight ≤ Server.maxInFlightPerConn` is enforced by the jrpc2 server options (`Concurrency` option)
- `len(out) ≤ cap(out) == 1024`; a full `out` triggers backpressure close via FR-024
- Every goroutine owned by this connection terminates before the `Server.wg.Done()` call
- Subscriptions in the map are released within 100 ms of connection close (FR-025)

---

### Subscription

Per-connection state recording a client's opt-in to server-initiated event notifications.

| Field | Type | Notes |
|---|---|---|
| `id` | `string` | Opaque identifier returned to the client from `subscribe`; UUID v4 |
| `events` | `map[string]struct{}` | Event type names the client opted into; filter set |
| `createdAt` | `time.Time` | |

**Lifecycle**:

```
created (via subscribe) → active → closed (via unsubscribe OR connection close OR server shutdown OR backpressure)
```

Only the connection that created the subscription can close or unsubscribe it. A subscription does not survive its connection.

---

### ShutdownController

Not a separate struct in Go — encoded as the tuple `(Server.ctx, Server.cancel, Server.wg, Server.shutdownDeadline)`. Documented here as a named concept for the state machine.

**Behavior**:

```
idle → cancelled → draining → expired|complete
```

- `cancelled` — `ctx` is cancelled; `listener.Close()` has been called
- `draining` — `wg.Wait()` is in progress under a `time.After(shutdownDeadline)` race
- `expired` — deadline hit first; surviving per-conn contexts are cancelled
- `complete` — `wg.Wait()` returned; socket unlinked; lock released

---

## Wire envelopes

All envelopes conform to JSON-RPC 2.0. This table is the authoritative shape; the full field-level contract is in [contracts/wire-protocol.md](contracts/wire-protocol.md).

### Request (client → server)

```json
{"jsonrpc":"2.0","id":<number|string>,"method":<string>,"params":<object|array|null>}
```

- `id` is optional; a request without `id` is a client-side notification (fire-and-forget) per JSON-RPC 2.0 §4.1.
- `method` MUST be present and of type string.
- `params` MUST be an object or array if present; scalar params are rejected with `-32600`.

### Response — success (server → client)

```json
{"jsonrpc":"2.0","id":<same as request>,"result":<any>}
```

### Response — error (server → client)

```json
{"jsonrpc":"2.0","id":<same as request|null>,"error":{"code":<int>,"message":<string>,"data":<any|omitted>}}
```

- `id` is `null` only when the server could not determine the request id (e.g., parse error).

### Notification (server → client, server-initiated)

```json
{"jsonrpc":"2.0","method":"event","params":{"schema":"wa.event/v1","type":<string>,"subscriptionId":<string>,...payload}}
```

- No `id` field — this is a one-way push per JSON-RPC 2.0 §4.1.
- `method` is always the literal string `"event"`.
- `params.schema` uses the format `wa.event/v<MAJOR>`; major-version bumps are breaking.
- `params.type` is the event type name the subscription filter keyed on.
- `params.subscriptionId` echoes the id the server returned from `subscribe`.

---

## Error code table

Full normative text in [contracts/wire-protocol.md](contracts/wire-protocol.md); this is a summary suitable for cross-referencing in code.

| Code | Name | Origin | Feature |
|---|---|---|---|
| `-32700` | Parse error | JSON-RPC spec | 004 |
| `-32600` | Invalid Request | JSON-RPC spec | 004 |
| `-32601` | Method not found | JSON-RPC spec | 004 |
| `-32602` | Invalid params | JSON-RPC spec | 004/005 |
| `-32603` | Internal error | JSON-RPC spec | 004 |
| `-32000` | PeerCredRejected | server | 004 |
| `-32001` | Backpressure | server | 004 |
| `-32002` | ShutdownInProgress | server | 004 |
| `-32003` | RequestTimeoutDuringShutdown | server | 004 |
| `-32004` | OversizedMessage | server | 004 |
| `-32005` | SubscriptionClosed | server | 004 |
| `-32006..-32010` | reserved | — | 004 future |
| `-32011..-32020` | **Reserved for feature 005 domain errors** | — | 005 |
| `-32021..-32099` | reserved | — | later features |

**Stability rule**: this table is append-only. Numbers never change once shipped. Names may be refined in docs but the wire semantics are fixed.

---

## Validation rules

Mapped from spec FRs to code-level checks:

| Rule | Source | Enforced at |
|---|---|---|
| Socket path is absolute and short enough for `sun_path` (≤104 bytes on darwin, ≤108 on linux) | FR-001, Edge Case "Socket path too long" | `listener.go` startup |
| Parent dir exists with mode `0700` | FR-002 | `listener.go` startup |
| Parent dir is not world-writable or group-writable | Edge Case | `listener.go` startup |
| Parent dir is not a symlink not created by the server | Edge Case | `listener.go` startup |
| Socket file has mode `0600` immediately after creation | FR-003 | `listener.go` startup |
| Every request envelope has `jsonrpc: "2.0"`, `method` string, valid `id` type if present | FR-004, FR-009 | jrpc2 framing layer |
| Each framed message ≤ 1 MiB | FR-005 | jrpc2 `channel.Line` wrapper |
| Peer uid equals server uid | FR-013, FR-014 | `accept.go` peer-cred gate |
| Only one server holds the lock | FR-016 | `lock.go` at startup |
| In-flight count per connection ≤ 32 | FR-028 | jrpc2 `Concurrency` option |
| Outbound mailbox ≤ 1024 | FR-024 | `connection.go` writer loop |

---

## Relationships

```
Server 1 ──owns──> 1 net.Listener
Server 1 ──owns──> 0..* Connection
Connection 1 ──has──> 1 peerUID
Connection 1 ──has──> 0..* Subscription
Connection 1 ──has──> 1 out mailbox (bounded)
Subscription 1 ──filters──> 0..* EventType names
Dispatcher 1 ──produces──> 1 Event source (<-chan Event)
Dispatcher 1 ──handles──> 0..* Request method invocations
```

`Dispatcher` is an interface, not a struct. The production implementation is written in feature 005; the test implementation `FakeDispatcher` is written in this feature in `sockettest/fake_dispatcher.go`.

---

## LOC budget (planned)

Rough per-file budget for the implementation phase. Total target ≤ 1500 LOC (excluding tests).

| File | Estimate |
|---|---|
| `server.go` | 150 |
| `listener.go` | 120 |
| `path_linux.go` + `path_darwin.go` | 30 + 30 |
| `peercred_linux.go` + `peercred_darwin.go` | 40 + 40 |
| `accept.go` | 100 |
| `connection.go` | 180 |
| `dispatch.go` | 100 |
| `dispatcher.go` (interface) | 40 |
| `errors.go` | 60 |
| `errcodes.go` | 80 |
| `subscribe.go` | 140 |
| `lifecycle.go` | 80 |
| `lock.go` | 60 |
| `doc.go` | 20 |
| **Subtotal (production)** | **1270** |
| `sockettest/suite.go` | 80 |
| `sockettest/fake_dispatcher.go` | 150 |
| `sockettest/helpers.go` | 100 |
| `sockettest/*_test.go` (5 files) | 600 |
| `sockettest/leak_test.go` | 30 |
| `server_test.go` (in-package) | 200 |
| **Subtotal (tests)** | **1160** |
| **Total** | **~2430** |

This is in line with the scope of a single feature — roughly comparable to feature 003's whatsmeow adapter (~2500 LOC).
