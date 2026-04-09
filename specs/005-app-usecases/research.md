# Research: Application Use Cases

**Feature**: 005-app-usecases
**Date**: 2026-04-09
**Status**: Complete

## D1 — Token bucket rate limiter

**Decision**: Use three independent `golang.org/x/time/rate.Limiter` instances (per-second, per-minute, per-day), checked sequentially. Warmup recalculates caps via `SetLimit`/`SetBurst`.

**Rationale**: `x/time/rate` is stdlib-adjacent, well-tested, thread-safe, and supports dynamic cap changes via `SetLimit()`/`SetBurst()` (both mutex-guarded). Three independent buckets are checked in sequence: if bucket 1 passes but bucket 2 rejects, the consumed token from bucket 1 is "wasted" — but this is conservative (tighter than necessary), which is safe for anti-ban purposes. No new dependency: `golang.org/x/time` is stdlib-adjacent and already transitively present.

**Alternatives considered**:
- Hand-rolled triple-bucket with `time.Ticker` + atomics — reinvents a well-tested wheel. Rejected.
- `uber-go/ratelimit` — leaky-bucket model (blocks callers, no reject). Wrong model for our "reject immediately" semantics. Rejected.
- `juju/ratelimit` — unmaintained. Rejected.

**Source**: [pkg.go.dev/golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate)

---

## D2 — Import direction: Dispatcher interface location

**Decision**: The app layer defines its OWN `Event` type in `internal/app/`. The `socket.Dispatcher` interface stays in `internal/adapters/primary/socket/`. The composition root (`cmd/wad`) wires them with a thin adapter that converts `app.Event` → `socket.Event`.

**Rationale**: Constitution principle I says `internal/app/` MUST NOT import adapter packages. The `socket.Event` type lives in `internal/adapters/primary/socket/dispatcher.go` — importing it from `internal/app/` would violate the hexagonal boundary. Go's structural typing means the app layer's concrete struct can satisfy the socket's `Dispatcher` interface IF the method signatures match exactly. The problem is `Events() <-chan socket.Event` — the app layer cannot reference `socket.Event`.

The clean solution is:
1. `internal/app/` defines `type AppEvent struct { Type string; Payload any }` and its dispatcher returns `<-chan AppEvent`
2. `internal/adapters/primary/socket/` defines `Dispatcher` with `Events() <-chan Event` (its own Event)
3. `cmd/wad/` contains a thin `dispatcherAdapter` that wraps the app dispatcher, converting `AppEvent` → `socket.Event` in a goroutine

This preserves the hexagonal invariant: core depends on nothing outside core; adapters depend on core; the composition root wires everything.

**Alternatives considered**:
- (a) Duplicate interface in `internal/app/` — acceptable but confusing. What we do instead is define the app-layer's own types, not a duplicate of socket's.
- (b) Shared package `internal/app/dispatch/` — adds an unnecessary package boundary for 2 types.
- (c) Accept the import from app → socket — violates constitution principle I. Rejected.

**Source**: Constitution principle I; CLAUDE.md rule 22 ("No infrastructure types in port signatures"); Cockburn's hexagonal architecture (2005): adapters depend on core, not the reverse.

---

## D3 — markRead port shape

**Decision**: Add `MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error` to the `MessageSender` port interface.

**Rationale**: Upstream whatsmeow exposes `Client.MarkRead(ctx, ids, timestamp, chat, sender)`. The domain `Message` sum type has no `ReadReceiptMessage` variant — and adding one would be semantically wrong (a read receipt is not a message). The `MessageSender` port is the outbound-action port grouping all write operations to WhatsApp. Adding `MarkRead` to it keeps the port count stable at 8 and groups related capabilities.

The whatsmeow adapter already has the whatsmeow `Client` reference; adding `MarkRead` to the `whatsmeowClient` interface and implementing the method is a one-liner delegation. The in-memory fake in `memory/adapter.go` gets a no-op `MarkRead` that records the call.

**Change required**: This is the ONLY ports.go change in feature 005. It adds one method to an existing interface.

**Alternatives considered**:
- New `ReadReceiptSender` port — adds a 9th port for a single method. Overengineered. Rejected.
- `ReadReceiptMessage` domain type routed through `Send` — semantically incorrect. Rejected.

**Source**: `go.mau.fi/whatsmeow` `Client.MarkRead` at `receipt.go:194`; `internal/app/ports.go` `MessageSender` interface.

---

## D4 — Event bridge and `wait` method design

**Decision**: Fan-out from the bridge goroutine. The bridge reads `EventStream.Next()` once, then delivers each event to BOTH the `Events()` channel AND a set of registered `wait` waiters.

**Rationale**: `Events()` returns a single channel consumed by the socket adapter (`dispatcher.go:56`: "called once at server startup"). The `wait` method cannot also read from this channel. Calling `EventStream.Next()` directly from `wait` would create two competing consumers on the same pull-based stream, violating the ES3/ES4 ordering guarantees in the port contract.

Design:
- The bridge goroutine reads `EventStream.Next(ctx)` in a loop
- For each event, it:
  1. Pushes to the `Events()` channel (non-blocking; if full, log warning)
  2. Iterates registered `wait` waiters (slice of `struct{filter []string; ch chan AppEvent}` protected by mutex); delivers to any waiter whose filter matches `event.Type`
- `wait` registers a waiter, blocks on its channel with `context.WithTimeout`, deregisters on return
- Deregistration is in a `defer` so it's safe on timeout, cancel, and success paths

This preserves the single-consumer invariant on `EventStream` and the single-channel contract on `Events()`.

**Alternatives considered**:
- (b) `wait` calls `EventStream.Next()` directly — two consumers on a pull-based stream violates port contract. Rejected.
- (a) `wait` reads from `Events()` — channel has one consumer (socket adapter). Rejected.

**Source**: `internal/app/ports.go` EventStream contract (ES1-ES6); `internal/adapters/primary/socket/dispatcher.go:56`.

---

## D5 — Audit event construction

**Decision**: Use `domain.NewAuditEvent(actor, action, subject, decision, detail)`.

**Rationale**: Reading `internal/domain/audit.go`: `AuditEvent` has fields `ID` (EventID, assigned by the adapter), `TS` (time.Time, stamped by `NewAuditEvent`), `Actor` (string), `Action` (AuditAction), `Subject` (JID), `Decision` (string), `Detail` (string). Available `AuditAction` constants: `AuditSend`, `AuditReceive`, `AuditPair`, `AuditGrant`, `AuditRevoke`, `AuditPanic`.

Pattern for use cases:
- Success: `NewAuditEvent("dispatcher", AuditSend, targetJID, "ok", "messageId=xyz")`
- Allowlist deny: `NewAuditEvent("dispatcher", AuditSend, targetJID, "denied:allowlist", "")`
- Rate limit deny: `NewAuditEvent("dispatcher", AuditSend, targetJID, "denied:rate", "")`
- Warmup deny: `NewAuditEvent("dispatcher", AuditSend, targetJID, "denied:warmup", "")`

Per FR-037, audit write failures are logged at ERROR but do not fail the request (best-effort).

**Source**: `internal/domain/audit.go:46-57`.

---

## Summary of changes to existing code

| File | Change | Why |
|---|---|---|
| `internal/app/ports.go` | Add `MarkRead(ctx, chat JID, id MessageID) error` to `MessageSender` | D3: read receipts need a port method |
| `internal/adapters/secondary/whatsmeow/whatsmeow_client.go` | Add `MarkRead` to the `whatsmeowClient` interface | D3: mirrors the port change |
| `internal/adapters/secondary/whatsmeow/send.go` (or new `markread.go`) | Implement `Adapter.MarkRead` delegating to `client.MarkRead` | D3 |
| `internal/adapters/secondary/memory/adapter.go` | Add no-op `MarkRead` to the in-memory fake | D3: tests need the fake |
| No change to `internal/adapters/primary/socket/dispatcher.go` | The socket's `Dispatcher` stays as-is; wiring is in `cmd/wad` | D2 |

No new dependencies are introduced. `golang.org/x/time/rate` is the only addition (stdlib-adjacent, already in the Go module proxy).

---

## D6 — testing/synctest for rate-limiter tests

**Decision**: Use `testing/synctest` (Go 1.25 GA) for rate-limiter and warmup tests. Unlike feature 004 (which rejected synctest because real socket I/O syscalls don't advance the fake clock), feature 005's rate limiter and warmup are pure in-memory computation using `x/time/rate.Limiter.Allow()` and `time.Now()` — both of which are intercepted by synctest's fake clock.

**Rationale**: The rate limiter's per-second bucket has a 2 token/s rate. Testing that the (N+1)th request is rejected after the burst requires waiting for tokens to refill. With `time.Sleep` this takes real wall time and is flaky under CI load. With synctest, `time.Sleep(time.Second)` inside a bubble advances the fake clock instantly, making the test deterministic and fast.

**Source**: [pkg.go.dev/testing/synctest](https://pkg.go.dev/testing/synctest); feature 004 research.md D12 (precedent for synctest usage in this project).

---

## D7 — Depguard rule for app-no-adapters

**Decision**: Add a depguard rule `app-no-adapters` to `.golangci.yml` forbidding imports matching `github.com/yolo-labz/wa/internal/adapters/**` from files under `**/internal/app/**`.

**Rationale**: The existing `core-no-whatsmeow` rule forbids whatsmeow imports from the core, but does not prevent the core from importing the socket adapter or any other primary/secondary adapter package. Without this rule, a developer could accidentally import `internal/adapters/primary/socket` from `internal/app/`, violating the hexagonal boundary (constitution principle I). The new rule mechanically enforces the core→adapter direction.

**Source**: Constitution principle I; `.golangci.yml` existing `core-no-whatsmeow` rule pattern.
