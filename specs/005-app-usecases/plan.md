# Implementation Plan: Application Use Cases

**Branch**: `005-app-usecases` | **Date**: 2026-04-09 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/005-app-usecases/spec.md`

## Summary

Deliver the application use case layer: a concrete `AppDispatcher` struct in `internal/app/` that implements the `socket.Dispatcher` interface (via composition-root adapter per D2) with method routing, safety middleware (allowlist + rate limiter + warmup), event bridge, and audit logging. The rate limiter uses three `golang.org/x/time/rate.Limiter` instances (D1). The `MarkRead` method extends the `MessageSender` port (D3). The `wait` method fans out from the event bridge goroutine (D4). All tests use in-memory fakes — no whatsmeow dependency in the app layer.

## Technical Context

**Language/Version**: Go 1.25 (toolchain pinned in `go.mod`)
**Primary Dependencies**:
- `golang.org/x/time/rate` — three independent token-bucket rate limiters with dynamic cap adjustment for warmup
- No other new dependencies. All domain types, port interfaces, and in-memory fakes already exist from features 002-003.

**Storage**: None. Rate limiter state is in-memory and resets on restart.
**Testing**: `go test -race ./internal/app/...` using `internal/adapters/secondary/memory/` fakes. `go.uber.org/goleak` for goroutine leak detection in `TestMain`. `testing/synctest` (Go 1.25+) for rate-limiter and warmup tests — unlike feature 004's socket tests (which needed real I/O syscalls), the rate limiter and warmup are pure in-memory computation where synctest's fake clock is ideal for deterministic timing assertions without `time.Sleep`.
**Target Platform**: Linux and macOS (same Go code, no OS-specific files).
**Project Type**: Library package inside the monorepo. Feature 006 constructs it from `cmd/wad`.
**Performance Goals**: `send` against fakes < 5ms; event bridge 1000 events/s; test suite < 5s.
**Constraints**: `CGO_ENABLED=0`; no whatsmeow imports; no adapter imports from `internal/app/`. A new depguard rule `app-no-adapters` will mechanically enforce the core-to-adapter boundary (FR-042).

## Constitution Check

| # | Principle | Status | Notes |
|---|---|---|---|
| I | Hexagonal core | ✅ PASS | `internal/app/` imports only `internal/domain/` and stdlib. The `socket.Dispatcher` interface is satisfied via a thin adapter in `cmd/wad` (D2), preserving the dependency direction. |
| II | Daemon owns state | ✅ PASS | Use cases hold no persistent state. Rate limiter is in-memory. Session timestamp is injected. |
| III | Safety first | ✅ PASS | **This feature materializes principle III.** Allowlist (default deny), rate limiter (2/s, 30/min, 1000/day), warmup (25/50/100%), audit log — all wired before the first `Send`. |
| IV | CGO forbidden | ✅ PASS | `x/time/rate` is pure Go. No new CGO dependency. |
| V | Spec-driven with citations | ✅ PASS | 5 D-blocks in research.md, all cited. |
| VI | Port-boundary fakes | ✅ PASS | Every test uses `internal/adapters/secondary/memory/` fakes. No integration gate needed. |
| VII | Conventional commits | ✅ PASS | Inherited from PR #1 lefthook. |

## Project Structure

### Documentation

```text
specs/005-app-usecases/
├── plan.md              # This file
├── research.md          # D1..D5 decisions
├── data-model.md        # Entities, state machines, LOC budget
├── quickstart.md        # 8-step reproducible verification
├── contracts/
│   ├── dispatcher-impl.md  # AppDispatcher contract (method table, safety pipeline, event bridge)
│   └── rate-limiter.md     # Rate limiter + warmup contract
├── checklists/
│   └── requirements.md  # Spec quality checklist (already written)
└── tasks.md             # /speckit:tasks output
```

### Source Code

```text
internal/app/
├── ports.go                 # EXISTING (feature 002) — add MarkRead to MessageSender (D3)
├── dispatcher.go            # NEW — AppDispatcher struct, constructor, Handle, Events, Close
├── method_send.go           # NEW — send, sendMedia, react handlers
├── method_pair.go           # NEW — pair handler
├── method_status.go         # NEW — status, groups handlers
├── method_markread.go       # NEW — markRead handler
├── method_wait.go           # NEW — wait handler with fan-out waiter
├── safety.go                # NEW — SafetyPipeline: allowlist + rate limiter + warmup
├── ratelimiter.go           # NEW — RateLimiter wrapping 3x rate.Limiter + warmup multiplier
├── eventbridge.go           # NEW — bridge goroutine: EventStream.Next → Events() + wait fan-out
├── events.go                # NEW — AppEvent type (app-layer event, distinct from socket.Event per D2)
├── errors.go                # NEW — typed errors implementing codedError for JSON-RPC mapping
├── dispatcher_test.go       # NEW — integration tests: full pipeline with fakes
├── safety_test.go           # NEW — unit tests: allowlist deny, rate limit, warmup
├── ratelimiter_test.go      # NEW — unit tests: token bucket behavior, warmup multiplier
├── eventbridge_test.go      # NEW — unit tests: bridge delivery, wait fan-out, shutdown
└── method_send_test.go      # NEW — unit tests: send/sendMedia/react param parsing + safety

internal/adapters/secondary/memory/
└── adapter.go               # EXISTING — add MarkRead no-op

internal/adapters/secondary/whatsmeow/
├── whatsmeow_client.go      # EXISTING — add MarkRead to interface
└── markread.go              # NEW — Adapter.MarkRead delegating to client.MarkRead
```

**Structure Decision**: All new code lives in `internal/app/` (the existing use case package) plus minimal changes to 3 existing files. No new top-level directories. The file-per-method pattern keeps each use case handler small and independently reviewable.

## Complexity Tracking

No constitution violations. One cross-feature change is justified:

| Change | Why | Simpler alternative rejected |
|---|---|---|
| Add `MarkRead` to `MessageSender` port (ports.go) | Read receipts are an outbound write action that needs the same safety pipeline as Send. A separate port would be a 9th port for a single method. | `ReadReceiptMessage` domain type routed through `Send` — semantically wrong (a receipt is not a message). |
