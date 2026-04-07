# Contract: the seven port interfaces

**Applies to**: `internal/app/ports.go`
**Enforced by**: spec FR-007, FR-008, FR-009; contract test suite under `internal/app/porttest/`.

This file is the **canonical Go signature** for every port interface, plus the behavioural contract every implementation must satisfy. It is the literal source of `internal/app/ports.go` (modulo the `package` and `import` blocks). Any divergence between this file and `ports.go` is a documentation defect to fix in the same commit.

## Package, imports, header

```go
// Package app contains the application use cases and the seven port interfaces
// that bound the hexagonal core. Files under this package MUST NOT import
// "go.mau.fi/whatsmeow" or any of its subpackages — the .golangci.yml depguard
// rule "core-no-whatsmeow" enforces this.
package app

import (
    "context"
    "time"

    "github.com/yolo-labz/wa/internal/domain"
)
```

`context` is the only stdlib import for I/O signatures. `time` is needed for the audit log timestamp parameter and for the receipt event lookups. `internal/domain` is the only project import. **No other imports are permitted in `ports.go`.**

## 1. `MessageSender`

```go
// MessageSender is the secondary port for outbound message delivery.
//
// Implementations MUST:
//   - Validate msg.Validate() before any I/O; return the validation error
//     wrapped via fmt.Errorf("MessageSender.Send: %w", err) without contacting
//     any external system.
//   - Honour ctx cancellation. If ctx.Done() fires before delivery, return
//     ctx.Err() (which will be context.Canceled or context.DeadlineExceeded).
//   - Return a non-zero MessageID on success. The ID format is opaque to
//     callers; only its non-zeroness and round-trip equality are guaranteed.
//   - Return a typed error wrapping one of: domain.ErrInvalidJID,
//     domain.ErrMessageTooLarge, domain.ErrEmptyBody, or one of the adapter's
//     own infrastructure errors.
//   - Be safe for concurrent use by multiple goroutines.
type MessageSender interface {
    Send(ctx context.Context, msg domain.Message) (domain.MessageID, error)
}
```

**Behavioural contract** (each clause must be a contract test):

| # | Precondition | Action | Postcondition |
|---|---|---|---|
| MS1 | `msg.Validate()` returns nil | `Send(ctx, msg)` | returns non-zero `MessageID`, nil error |
| MS2 | `msg.Validate()` returns `ErrEmptyBody` | `Send(ctx, msg)` | returns zero `MessageID`, error wrapping `ErrEmptyBody`; no I/O performed |
| MS3 | `msg.Validate()` returns `ErrMessageTooLarge` | `Send(ctx, msg)` | returns zero `MessageID`, error wrapping `ErrMessageTooLarge`; no I/O performed |
| MS4 | ctx is already cancelled | `Send(ctx, msg)` | returns `context.Canceled` immediately |
| MS5 | two goroutines call `Send` in parallel | both calls | both succeed; no data race detected by `-race` |
| MS6 | `msg` is a `MediaMessage` with non-existent `Path` | `Send(ctx, msg)` | error wrapping a filesystem error; the error chain contains a path-not-found indicator (`os.ErrNotExist` or equivalent) |

## 2. `EventStream`

```go
// EventStream is the secondary port for inbound event delivery. It is
// pull-based by design: callers ask for the next event under a context, and
// the implementation blocks until one is available or ctx is done.
//
// Implementations MUST:
//   - Buffer at least one event between Next() calls so a slow consumer does
//     not lose events from a fast producer.
//   - Honour ctx cancellation: a pending Next() returns ctx.Err() when the
//     deadline expires or the parent cancels.
//   - Return events in the order they were observed, by EventID monotonicity.
//   - Allow exactly one consumer per EventStream instance. Concurrent Next()
//     calls on the same instance MAY return either event to either caller in
//     undefined order, but MUST NOT skip or duplicate events.
//   - Acknowledge consumed events via Ack() so the implementation can advance
//     its internal cursor and free buffered memory.
type EventStream interface {
    Next(ctx context.Context) (domain.Event, error)
    Ack(id domain.EventID) error
}
```

**Behavioural contract**:

| # | Precondition | Action | Postcondition |
|---|---|---|---|
| ES1 | one event has been enqueued by the test fixture | `Next(ctx)` | returns that event, nil error |
| ES2 | no event has been enqueued, ctx has 100ms deadline | `Next(ctx)` | returns nil event, `context.DeadlineExceeded` after ~100ms |
| ES3 | three events enqueued in order A, B, C | three `Next(ctx)` calls in sequence | return A, B, C in that order |
| ES4 | event A returned from `Next` but never `Ack`'d | next `Next(ctx)` | returns event B (the in-memory adapter does not require Ack to advance, but MUST NOT lose un-Ack'd events on adapter close) |
| ES5 | `Ack(unknownID)` | call | returns a typed error wrapping a "not found" indicator; never panics |
| ES6 | producer enqueues 1000 events while consumer reads | full sequence read | every event observed exactly once, no duplicates, no drops |

## 3. `ContactDirectory`

```go
// ContactDirectory is the secondary port for contact metadata lookup.
//
// Implementations MUST:
//   - Return a Contact with the requested JID's PushName and Verified flag,
//     or domain.ErrInvalidJID wrapped if the JID is malformed (which should
//     not be possible if domain.Parse was used to construct it).
//   - Return a typed error wrapping a "not found" indicator for unknown
//     contacts; the in-memory adapter returns a sentinel ErrContactNotFound
//     defined in internal/adapters/secondary/memory.
//   - Resolve a phone-string input to a JID via Resolve(); the in-memory
//     adapter parses it via domain.ParsePhone with no network call. The
//     whatsmeow adapter (feature 003) MAY query WhatsApp to verify
//     registration, but MUST honour ctx cancellation.
type ContactDirectory interface {
    Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error)
    Resolve(ctx context.Context, phone string) (domain.JID, error)
}
```

**Behavioural contract**: 4 cases (lookup-found, lookup-not-found, resolve-valid-phone, resolve-malformed-phone). Detailed in the contract test file.

## 4. `GroupManager`

```go
// GroupManager is the secondary port for group metadata lookup.
//
// Implementations MUST:
//   - Return Group values whose JID.IsGroup() is true, whose Participants are
//     all user JIDs, and whose Admins ⊆ Participants.
//   - Return a typed error wrapping "not found" for unknown group JIDs.
//   - List() MUST return a snapshot, not a live cursor; mutating the returned
//     slice MUST NOT affect the implementation's internal state.
type GroupManager interface {
    List(ctx context.Context) ([]domain.Group, error)
    Get(ctx context.Context, jid domain.JID) (domain.Group, error)
}
```

**Behavioural contract**: 4 cases. List on empty store returns empty slice + nil error (not nil slice + nil error — distinction matters for JSON output).

## 5. `SessionStore`

```go
// SessionStore is the secondary port for session persistence. The Session
// value is the domain's opaque handle; the actual Signal Protocol material
// (prekeys, ratchets, registration ID) lives entirely inside the
// implementation, never crossing this interface.
//
// Implementations MUST:
//   - Load() returns the most recently Save'd session, or a zero Session if
//     none has been saved (NOT an error).
//   - Save() persists the session atomically; concurrent Save() calls from
//     multiple goroutines MUST be serialised so the store is never observed
//     in a partially-written state.
//   - Clear() removes the persisted session and returns nil even if no
//     session was present (idempotent).
type SessionStore interface {
    Load(ctx context.Context) (domain.Session, error)
    Save(ctx context.Context, s domain.Session) error
    Clear(ctx context.Context) error
}
```

**Behavioural contract**: 6 cases including atomicity under parallel Save and the idempotency of Clear on an empty store.

## 6. `Allowlist`

```go
// Allowlist is the secondary port for the policy decision. It is the only
// port that does NOT take a context.Context: the decision is pure, in-memory,
// and synchronous by design (research.md D3). An implementation that needs
// I/O to make the decision is doing it wrong; the I/O belongs in a separate
// port that produces the Allowlist value.
type Allowlist interface {
    Allows(jid domain.JID, action domain.Action) bool
}
```

**Behavioural contract**:

| # | Precondition | Action | Postcondition |
|---|---|---|---|
| AL1 | empty allowlist | `Allows(any, any)` | returns false (default deny) |
| AL2 | jid granted ActionRead | `Allows(jid, ActionRead)` | returns true |
| AL3 | jid granted ActionRead | `Allows(jid, ActionSend)` | returns false (no implicit promotion) |
| AL4 | jid granted ActionSend then revoked | `Allows(jid, ActionSend)` | returns false |
| AL5 | concurrent reads from 8 goroutines for 1000 iterations | all calls | no data race; results consistent with the snapshot at call time |
| AL6 | unknown jid | `Allows(jid, any)` | returns false |

The contract is implemented by `*domain.Allowlist` directly; the in-memory adapter and the future whatsmeow adapter both consume the same value type. This is the one port whose only "implementation" is the domain type itself — and that is the correct hexagonal layering per research.md D5.

## 7. `AuditLog`

```go
// AuditLog is the secondary port for the append-only audit log. Every send,
// every authorization decision, every pairing attempt produces an entry.
// The constitution mandates this log is separate from the debug log and
// never auto-rotated.
//
// Implementations MUST:
//   - Append entries in monotonic timestamp order. Out-of-order writes are
//     a programmer error and MUST return a typed error.
//   - Persist before Record returns. There is no buffering; the audit log is
//     more important than throughput. The future SQLite-backed implementation
//     uses synchronous writes (PRAGMA synchronous=FULL).
//   - Be safe for concurrent use; multiple goroutines may Record at once.
type AuditLog interface {
    Record(ctx context.Context, e domain.AuditEvent) error
}
```

**Behavioural contract**: 5 cases including monotonic-order enforcement and parallel-write safety.

## Forbidden patterns in `ports.go`

`golangci-lint` enforces these structurally; this list is the human-readable explanation:

| Pattern | Why forbidden |
|---|---|
| `import "go.mau.fi/whatsmeow"` | depguard `core-no-whatsmeow` rule; the whole point of this layer |
| `chan domain.Event` field on `EventStream` | Channels-as-cancellation is the pre-1.7 idiom `context` was designed to replace; pull-based via `Next(ctx)` is the design (research.md D3) |
| Returning `interface{}` or `any` from any port method | Breaks type safety; if the data is sum-typed, it gets a sealed interface in `internal/domain` |
| `panic()` | `forbidigo` rule bans panics outside `package main`; ports return errors |
| `fmt.Print*` | `forbidigo` rule bans printing outside `cmd/`; ports use the slog logger via the `AuditLog` port if they need to record |
| Storing `context.Context` on a struct field | Stdlib code review comment §contexts; contexts flow as parameters only |
| `context.TODO()` | Same; production code uses real contexts |

## Total surface

**7 interfaces, 13 methods, ~120 lines of `ports.go`** including doc comments. The contract test suite has at least one positive and one negative test per method, so `internal/app/porttest/` will contain ~30 test functions across 7 files.

## Mapping to JSON-RPC methods (from CLAUDE.md §"Daemon, IPC, single-instance")

The 11 JSON-RPC methods listed in CLAUDE.md map to ports as follows. This mapping is the answer to spec SC-001 (a maintainer can name the port for any RPC method in under 10 minutes).

| JSON-RPC method | Port(s) used | Notes |
|---|---|---|
| `pair` | `SessionStore` (write), `EventStream` (subscribe to PairingEvent) | |
| `status` | `SessionStore` (read), `EventStream` (last ConnectionEvent) | |
| `send` | `Allowlist` (decision), `MessageSender` (delivery), `AuditLog` (record) | |
| `sendMedia` | same as `send` | only the `Message` variant differs |
| `markRead` | `MessageSender` (the read-receipt is itself a special message) | |
| `react` | `MessageSender` (reaction is a `ReactionMessage` variant) | |
| `groups` | `GroupManager` | |
| `subscribe` | `EventStream` | |
| `wait` | `EventStream` | thin wrapper over `Next(ctx)` with a timeout |
| `allow` | `Allowlist` (`Grant`/`Revoke` on the `*domain.Allowlist`), `AuditLog` | |
| `panic` | `SessionStore.Clear`, `AuditLog`, indirect call into the whatsmeow adapter to unlink | |

Eleven RPC methods, seven ports, no overlap, no gap. The mapping is the executable form of "we picked the right port set."
