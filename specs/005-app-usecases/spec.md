# Feature Specification: Application Use Cases

**Feature Branch**: `005-app-usecases`
**Created**: 2026-04-09
**Status**: Draft
**Input**: User description: "Feature 005: Application use cases — implement the Dispatcher interface from feature 004 with concrete use case orchestrators (SendMessage, PairDevice, StreamEvents, ListGroups, StatusCheck, MarkRead). Wire allowlist middleware (default-deny), rate limiter middleware (per-second/per-minute/per-day caps), and warmup ramp (25/50/100 percent over first 14 days). Safety first per constitution principle III."

## Overview

This feature builds the application layer that connects the transport (feature 004's socket adapter) to the infrastructure (feature 003's secondary adapters) through the port interfaces declared in feature 002. The deliverable is a concrete implementation of the `socket.Dispatcher` interface: a struct that holds references to the 8 port interfaces, routes incoming JSON-RPC method names to use case functions, bridges the pull-based `EventStream` port to the push-based `Events()` channel, and wraps every outbound action in the safety stack mandated by constitution principle III (allowlist check, rate limiter, warmup gate, use case, audit log).

This is the feature where the "safety first, no `--force` ever" principle materializes as running code. The allowlist defaults to deny-all. The rate limiter enforces per-second (2/s), per-minute (30/min), and per-day (1000/day) caps with no override. The warmup ramp restricts a fresh session to 25% of those caps for the first 7 days and 50% for days 8-14. Every outbound action (send, sendMedia, markRead, react) is gated; read-only operations (status, groups) pass through ungated.

The use cases live in `internal/app/` and depend only on `internal/domain/` and the port interfaces in `ports.go`. They do not import `go.mau.fi/whatsmeow`, the socket adapter, or any other adapter. They are tested entirely with the in-memory fakes from `internal/adapters/secondary/memory/`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Send a message through the safety stack (Priority: P1)

As the daemon operator, when I invoke `wa send --to 5511999999999@s.whatsapp.net --body "hello"`, the CLI calls the `send` JSON-RPC method on the daemon. The dispatcher checks the allowlist (is this JID allowed for action `send`?), checks the rate limiter (am I under the per-second, per-minute, and per-day caps?), checks the warmup ramp (is the session old enough for full-rate sending?), and only then calls `MessageSender.Send`. An audit log entry records the decision (allow or deny) and the outcome (success or error). If any check fails, the caller receives a typed error code (not-allowlisted, rate-limited, or warmup-active) and no message is sent.

**Why this priority**: This is the primary value proposition of the daemon. Without it, the socket adapter has nothing to dispatch. The safety stack is constitutionally mandatory before the first Send.

**Independent Test**: With in-memory fakes for all 8 ports, an allowlisted JID, and the rate limiter under capacity, calling `Handle(ctx, "send", params)` returns a success result with a messageId. With a non-allowlisted JID, it returns a typed error. With the rate limiter exhausted, it returns a different typed error. Each path is independently verifiable without any other use case being implemented.

**Acceptance Scenarios**:

1. **Given** JID X is on the allowlist for action `send` and the rate limiter is under capacity, **When** a `send` request arrives for JID X with body "hello", **Then** the dispatcher calls `MessageSender.Send`, returns `{messageId, timestamp}`, and records an audit entry with action `send`, JID X, outcome "ok".
2. **Given** JID Y is NOT on the allowlist for action `send`, **When** a `send` request arrives for JID Y, **Then** the dispatcher returns a typed not-allowlisted error (mapped to `-32012` by the socket adapter), does NOT call `MessageSender.Send`, and records an audit entry with action `send`, JID Y, outcome "denied:allowlist".
3. **Given** the rate limiter's per-minute bucket is exhausted, **When** a `send` request arrives for an allowlisted JID, **Then** the dispatcher returns a typed rate-limited error (mapped to `-32013`), does NOT call `MessageSender.Send`, and records an audit entry with outcome "denied:rate".
4. **Given** the session is 3 days old and the warmup ramp is at 25%, **When** 6 sends arrive in 1 second (exceeding 25% of the 2/s cap = 0.5/s effective cap), **Then** the 2nd through 6th sends return a typed warmup-active error (mapped to `-32014`).
5. **Given** a `sendMedia` request with a valid path and allowlisted JID, **When** dispatched, **Then** the same safety pipeline applies, and the result contains a messageId.

---

### User Story 2 — Pair a device through the daemon (Priority: P1)

As a first-time user, I run `wa pair` which sends a `pair` JSON-RPC method to the daemon. The dispatcher delegates to the whatsmeow adapter's pairing flow (QR-in-terminal by default, phone-code if a phone number is provided). Pairing does not go through the allowlist or rate limiter — it is a privileged setup action gated only by the peer-credential check at the socket layer.

**Why this priority**: Without pairing, the daemon cannot connect to WhatsApp. This must work before any other use case.

**Independent Test**: With the in-memory fake implementing the pairing method (which returns immediately with a "paired" sentinel), calling `Handle(ctx, "pair", {})` returns `{paired: true}`. The fake does not need a real WhatsApp account.

**Acceptance Scenarios**:

1. **Given** no device is currently paired, **When** a `pair` request arrives with no phone field, **Then** the dispatcher invokes the QR pairing flow and returns `{paired: true}` on success.
2. **Given** no device is currently paired, **When** a `pair` request arrives with `{phone: "+5511999999999"}`, **Then** the dispatcher invokes the phone-code flow and returns `{paired: true, code: "<8-char-code>"}`.
3. **Given** a device is already paired, **When** a `pair` request arrives, **Then** the dispatcher returns a typed already-paired error.

---

### User Story 3 — Stream inbound WhatsApp events (Priority: P2)

As the future Claude Code plugin (or any subscribing client), I need the dispatcher to continuously read events from the `EventStream` port (a pull-based Next(ctx) call) and push them onto the `Events()` channel so the socket adapter can fan them out to subscribers. The event bridge must not block, must not leak goroutines on shutdown, and must translate domain events into the `socket.Event` shape (Type string + Payload).

**Why this priority**: Required for the `subscribe` and `wa wait` flows, but the daemon can pair + send without it.

**Independent Test**: The in-memory EventStream fake is preloaded with 3 events. After the dispatcher starts its event bridge goroutine, reading 3 items from `Events()` yields the same events (translated to socket.Event shape). Shutting down the dispatcher closes the Events() channel without leaking goroutines.

**Acceptance Scenarios**:

1. **Given** the EventStream has a pending MessageEvent, **When** the bridge goroutine reads it, **Then** it appears on `Events()` as `{Type: "message", Payload: ...}`.
2. **Given** the EventStream has a pending ConnectionEvent, **When** the bridge goroutine reads it, **Then** it appears as `{Type: "status", Payload: ...}`.
3. **Given** the dispatcher's context is cancelled (shutdown), **When** the bridge goroutine exits, **Then** the `Events()` channel is closed and no goroutines leak.
4. **Given** a `wait` request arrives with `{events: ["message"], timeoutMs: 5000}`, **When** a message event arrives within 5 seconds, **Then** the dispatcher returns the event as the response result. If no event arrives within the timeout, the dispatcher returns a timeout error.

---

### User Story 4 — Query daemon state (Priority: P2)

As the operator running `wa status` or `wa groups`, I need the dispatcher to serve read-only queries that do not go through the safety stack (no allowlist, no rate limiter). These are informational and must be fast.

**Why this priority**: Essential for operational visibility, but not blocking for the core send/pair flow.

**Independent Test**: Calling `Handle(ctx, "status", {})` returns `{connected: true/false, jid: "...", ...}` from the in-memory fake. Calling `Handle(ctx, "groups", {})` returns the list from the GroupManager fake.

**Acceptance Scenarios**:

1. **Given** the daemon is connected, **When** a `status` request arrives, **Then** the dispatcher returns `{connected: true, jid: "<user-jid>"}` without consulting the allowlist or rate limiter.
2. **Given** the daemon is not connected, **When** a `status` request arrives, **Then** the dispatcher returns `{connected: false}`.
3. **Given** the user belongs to 3 groups, **When** a `groups` request arrives, **Then** the dispatcher returns `{groups: [{jid, subject, participants}, ...]}` with all 3 groups.

---

### User Story 5 — Warmup ramp for fresh sessions (Priority: P3)

As the operator with a freshly paired device, the rate limiter automatically applies a warmup schedule: 25% of normal caps for days 1-7 of the session, 50% for days 8-14, and 100% thereafter. This prevents WhatsApp from banning the account for aggressive automation on a new session. The warmup is not configurable and has no override.

**Why this priority**: Important for account safety but can be added after the basic rate limiter works. The basic limiter (US1) enforces hard caps; warmup merely scales them down.

**Independent Test**: A rate limiter configured with a session age of 3 days allows at most 25% of the per-second cap (0.5/s effectively). The same limiter with age 10 days allows 50%. With age 15 days, 100%.

**Acceptance Scenarios**:

1. **Given** a session created today, **When** the warmup ramp is computed, **Then** the effective per-second cap is 0.5/s (25% of 2/s), per-minute is 7 (25% of 30), per-day is 250 (25% of 1000).
2. **Given** a session created 10 days ago, **When** the warmup ramp is computed, **Then** the effective caps are 1/s, 15/min, 500/day (50%).
3. **Given** a session created 15 days ago, **When** the warmup ramp is computed, **Then** the effective caps are the full 2/s, 30/min, 1000/day.
4. **Given** the warmup has no override flag, **When** the operator attempts to bypass it, **Then** no mechanism exists to do so.

---

### Edge Cases

- **Nil or empty params**: A `send` request with nil params returns `-32602 Invalid params`, not a panic. Same for every method.
- **Unknown method**: The dispatcher returns a method-not-found error, letting the socket adapter map it to `-32601`.
- **Concurrent sends from multiple connections**: The rate limiter is shared across all connections (server-wide resource, not per-connection). Two connections sending simultaneously share the same per-second bucket.
- **EventStream returns error**: The bridge goroutine logs the error and retries `Next(ctx)`. If the error is `context.Canceled`, the bridge exits cleanly.
- **Audit log write failure**: The use case logs the audit failure at ERROR level but does NOT fail the user's request. Audit is best-effort; the message was already sent.
- **Rate limiter state across restarts**: Resets on daemon restart. Per-day counts do not persist. Acceptable because WhatsApp's own rate detection operates on a longer window.
- **`markRead` and `react` for a JID not on the allowlist**: Denied, same as `send`. These are outbound actions.
- **Empty allowlist (no entries)**: Default deny means ALL sends are rejected. This is the intended initial state — the operator must explicitly add JIDs via `wa allow add` before the daemon will send anything. The error message should hint at this.
- **`pair` when session already exists**: Returns a typed already-paired error. The user must call `panic` (unlink device) first to re-pair.
- **`wait` with no matching event before timeout**: Returns a timeout error (not an empty result).
- **`wait` waiter channel overflow**: The waiter channel has capacity 1. If two events match the filter before the `wait` caller reads, the second is dropped (the caller only wants the first matching event anyway).
- **Method `allow` and `panic`**: Not registered in the method table. Returns method-not-found, same as any unknown method. Reserved for feature 006.

## Requirements *(mandatory)*

### Functional Requirements

#### Dispatcher implementation

- **FR-001**: The dispatcher MUST implement the `socket.Dispatcher` interface: `Handle(ctx, method string, params json.RawMessage) (json.RawMessage, error)` and `Events() <-chan Event`.
- **FR-002**: The dispatcher MUST accept references to all 8 port interfaces (MessageSender, EventStream, ContactDirectory, GroupManager, SessionStore, Allowlist, AuditLog, HistoryStore) at construction time.
- **FR-003**: The dispatcher MUST route incoming method names to use case functions via an internal method table. Unknown methods MUST return an error that the socket adapter maps to `-32601 Method not found`.
- **FR-004**: The dispatcher MUST be safe for concurrent use by multiple goroutines.

#### Send message (text, media, reaction, markRead)

- **FR-005**: The `send` method MUST parse params `{to: string, body: string}`, validate the `to` field as a JID, and delegate to `MessageSender.Send` with a `domain.TextMessage`.
- **FR-006**: The `sendMedia` method MUST parse params `{to: string, path: string, caption?: string, mime?: string}` and delegate to `MessageSender.Send` with a `domain.MediaMessage`.
- **FR-007**: The `react` method MUST parse params `{chat: string, messageId: string, emoji: string}` and delegate to `MessageSender.Send` with a `domain.ReactionMessage`.
- **FR-008**: The `markRead` method MUST parse params `{chat: string, messageId: string}` and delegate to the appropriate port method for read receipts.
- **FR-009**: For `send`, `sendMedia`, `react`, and `markRead`, the dispatcher MUST execute the safety pipeline in this exact order: (1) parse params, (2) check allowlist, (3) check rate limiter, (4) call the port method, (5) record audit log entry. If any step fails, subsequent steps MUST NOT execute.
- **FR-010**: On success, `send` and `sendMedia` MUST return `{messageId: string, timestamp: int64}`. `react` and `markRead` MUST return `{}` (empty object).

#### Safety middleware — Allowlist

- **FR-011**: The dispatcher MUST check `Allowlist.Allows(jid, action)` before every outbound action.
- **FR-012**: If the allowlist denies the action, the dispatcher MUST return a typed not-allowlisted error and MUST NOT invoke the port method.
- **FR-013**: Read-only methods (`status`, `groups`, `pair`) MUST NOT consult the allowlist.

#### Safety middleware — Rate limiter

- **FR-014**: The dispatcher MUST enforce rate limits on every outbound action using three independent token buckets: per-second (default: 2), per-minute (default: 30), per-day (default: 1000).
- **FR-015**: The rate limiter MUST be shared across all connections.
- **FR-016**: If any bucket is exhausted, the dispatcher MUST return a typed rate-limited error and MUST NOT invoke the port method.
- **FR-017**: Read-only methods MUST NOT consume rate limiter tokens.
- **FR-018**: There is no `--force` flag, no admin override, and no per-request exemption.

#### Safety middleware — Warmup ramp

- **FR-019**: The dispatcher MUST accept a session creation timestamp at construction. If the session is younger than 7 days, the effective caps are 25%. If 7-14 days, 50%. If older than 14 days, 100%.
- **FR-020**: The warmup multiplier MUST be applied to all three buckets.
- **FR-021**: The warmup is not configurable and has no override.
- **FR-022**: If the warmup gate rejects a request, the dispatcher MUST return a typed warmup-active error.

#### Device pairing

- **FR-023**: The `pair` method MUST parse params `{phone?: string}`. If phone is empty, invoke QR flow. If present, invoke phone-code flow.
- **FR-024**: Pairing MUST NOT go through the allowlist, rate limiter, or warmup checks.
- **FR-025**: If a session already exists, the dispatcher MUST return a typed already-paired error.
- **FR-026**: On success, return `{paired: true}`. For phone-code flow, also `{code: "<8-char>"}`.

#### Status and groups

- **FR-027**: The `status` method MUST return `{connected: bool, jid?: string}` without the safety pipeline.
- **FR-028**: The `groups` method MUST return `{groups: [{jid, subject, participants}]}` without the safety pipeline.

#### Wait

- **FR-029**: The `wait` method MUST parse params `{events?: [string], timeoutMs?: int}` (defaults: all events, 30000ms).
- **FR-030**: The dispatcher MUST block until a matching event arrives, then return it. On timeout, return a timeout error.
- **FR-031**: `wait` MUST NOT consume rate limiter tokens.

#### Event bridge

- **FR-032**: The dispatcher MUST run a bridge goroutine: `EventStream.Next(ctx)` in a loop, translating each `domain.Event` to an `AppEvent{Type, Payload}` (the app-layer event type, per research D2) and pushing onto the `Events()` channel. The bridge also delivers each event to registered `wait` waiters whose filter matches the event type.
- **FR-033**: Type mapping: `MessageEvent` to `"message"`, `ReceiptEvent` to `"receipt"`, `ConnectionEvent` to `"status"`, `PairingEvent` to `"pairing"`.
- **FR-034**: On context cancellation, the bridge MUST exit and close the `Events()` channel without leaking goroutines.
- **FR-035**: On `EventStream.Next` error (not `context.Canceled`), log and retry after 100ms backoff.

#### Audit logging

- **FR-036**: Every outbound action MUST produce one audit entry via `AuditLog.Record` with: action, target JID, outcome, timestamp. Audit entries MUST be recorded for BOTH denials (allowlist, rate limit, warmup) AND successes — not just successes. The audit entry is recorded AFTER the pipeline decision but BEFORE the error/result is returned to the caller.
- **FR-037**: Audit write failures are logged at ERROR but do NOT fail the request.
- **FR-038**: Read-only methods and pairing do NOT produce audit entries.
- **FR-041**: Methods `allow` and `panic` MUST NOT be registered in the method table. Requests for these methods MUST return method-not-found, same as any other unknown method. Feature 006 will add them when it builds the composition root.
- **FR-042**: A depguard rule `app-no-adapters` MUST be added to `.golangci.yml` forbidding `internal/adapters/**` imports from `internal/app/**`, mechanically enforcing the hexagonal boundary in the core-to-adapter direction.

#### Error translation

- **FR-039**: The dispatcher MUST return typed errors implementing the `codedError` interface so the socket adapter maps them to JSON-RPC codes: not-allowlisted to `-32012`, rate-limited to `-32013`, warmup-active to `-32014`, not-paired to `-32011`, invalid-JID to `-32015`, message-too-large to `-32016`, disconnected to `-32018`.
- **FR-040**: Errors from port methods MUST be wrapped with context but MUST NOT expose adapter-internal details on the wire.

### Key Entities

- **AppDispatcher** — the struct implementing `socket.Dispatcher`. Holds the 8 port references, the safety middleware, the event bridge goroutine, and the Events() channel.
- **SafetyPipeline** — the composed middleware chain: allowlist check, rate limiter, warmup gate. Applied to every outbound method before the port call.
- **RateLimiter** — three independent token buckets with a warmup multiplier. Server-wide, thread-safe.
- **WarmupRamp** — a pure function returning 0.25, 0.50, or 1.0 based on session age.
- **EventBridge** — the goroutine that reads EventStream.Next and pushes socket.Event values.
- **MethodTable** — an immutable map from method name to handler function.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A `send` request against in-memory fakes with an allowlisted JID completes in under 5 ms.
- **SC-002**: A `send` request for a non-allowlisted JID returns the error within 1 ms without calling the port method.
- **SC-003**: The rate limiter rejects the (N+1)th request per second correctly, verified by a deterministic test.
- **SC-004**: Warmup caps are exactly 25%/50%/100% at day boundaries 0/7/14, verified by table-driven test.
- **SC-005**: The event bridge delivers 1000 events from the in-memory EventStream to Events() within 1 second.
- **SC-006**: Shutting down the dispatcher leaks zero goroutines (goleak).
- **SC-007**: Every outbound action produces exactly one audit log entry.
- **SC-008**: `go test -race ./internal/app/...` passes with zero race warnings.
- **SC-009**: The test suite runs in under 5 seconds wall clock.
- **SC-010**: Zero `go.mau.fi/whatsmeow` imports in `internal/app/` (depguard).

## Assumptions

- Rate limiter state does not persist across daemon restarts.
- Warmup timestamp is provided at construction by the composition root (feature 006).
- Methods `allow` and `panic` are deferred to feature 006.
- The in-memory fakes from feature 002 are sufficient for all tests.
- Audit log entries for denied requests include the denial reason.
- The `markRead` port method shape is resolved during the plan phase (may require extending `MessageSender` or adding a thin adapter method).

## Dependencies

- **Feature 002** — port interfaces and domain types. No changes.
- **Feature 003** — production secondary adapter (runtime only, not imported from `internal/app/`).
- **Feature 004** — `socket.Dispatcher` interface and `socket.Event` type.
- **Feature 006** — composition root (will construct AppDispatcher with real adapters).

## Out of Scope

- The `allow` method (allowlist mutation + TOML persistence) — feature 006.
- The `panic` method (device unlink + session clear) — feature 006.
- `cmd/wad` and `cmd/wa` binaries — feature 006.
- Persistent rate limiter state.
- Configurable rate limits (v0.2).
- Group creation, participant addition, broadcast lists.
