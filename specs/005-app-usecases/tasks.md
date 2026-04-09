---
description: "Implementation tasks for feature 005 — application use cases"
---

# Tasks: Application Use Cases

**Input**: Design documents from `/specs/005-app-usecases/`
**Prerequisites**: plan.md, spec.md (5 user stories), research.md (D1..D7), data-model.md, contracts/{dispatcher-impl,rate-limiter}.md

**Tests**: REQUIRED. The use case layer is the safety-critical component (constitution principle III). Every safety path must have a test.

**Organization**: 7 phases, 52 tasks, 6 commit boundaries.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1..US5)

---

## Phase 1: Setup

**Purpose**: Add dependencies, extend ports, update fakes, add depguard rule.

**Commit boundary**: `chore(005): bootstrap app use cases — extend ports, add x/time/rate, depguard rule`

- [x] T001 Add `golang.org/x/time` to `go.mod`; run `go get golang.org/x/time/rate@latest && go mod tidy` (FR-014, research D1)
- [x] T002 Add `MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error` to `MessageSender` in `internal/app/ports.go` (FR-008, research D3)
- [x] T003 [P] Add no-op `MarkRead` to `internal/adapters/secondary/memory/adapter.go` returning nil; record the call for test assertions (research D3)
- [x] T004 [P] Add `MarkRead` to the `whatsmeowClient` interface in `internal/adapters/secondary/whatsmeow/whatsmeow_client.go` (research D3)
- [x] T005 [P] Implement `Adapter.MarkRead` in `internal/adapters/secondary/whatsmeow/markread.go` delegating to `client.MarkRead` (research D3)
- [x] T006 [P] Add depguard rule `app-no-adapters` to `.golangci.yml` forbidding `github.com/yolo-labz/wa/internal/adapters` imports from `**/internal/app/**` (FR-042, research D7)
- [x] T007 Verify `go build ./...` and `go test -race ./...` pass with the extended port

**Checkpoint**: Ports extended, fakes updated, depguard hardened. All existing tests still pass.

---

## Phase 2: Foundational (Blocking prerequisites)

**Purpose**: Build the shared types, errors, rate limiter, safety pipeline, and event bridge that every user story needs.

**⚠️ CRITICAL**: No user-story task may start until this phase is complete.

**Commit boundary**: `feat(005): add AppEvent, errors, rate limiter, safety pipeline, event bridge`

### Types and errors

- [x] T008 Implement `AppEvent` struct in `internal/app/events.go` with `Type string` and `Payload any` fields (research D2, data-model §AppEvent)
- [x] T009 [P] Implement typed errors (`ErrNotAllowlisted`, `ErrRateLimited`, `ErrWarmupActive`, `ErrNotPaired`, `ErrInvalidJID`, `ErrMessageTooLarge`, `ErrDisconnected`, `ErrWaitTimeout`, `ErrMethodNotFound`) in `internal/app/errors.go`; each implements `codedError` interface with `RPCCode() int` returning the correct JSON-RPC code from data-model §Typed errors (FR-039)

### Rate limiter + warmup

- [x] T010 Implement `RateLimiter` struct in `internal/app/ratelimiter.go`: three `rate.Limiter` instances (per-second 2, per-minute 30, per-day 1000), `warmupMultiplier` pure function, `NewRateLimiter(sessionCreated time.Time)` constructor that computes multiplier and calls `SetLimit`/`SetBurst` (FR-014..FR-022, contracts/rate-limiter.md)
- [x] T011 [P] Implement `ratelimiter_test.go` in `internal/app/`: table-driven tests for (a) burst exhaustion, (b) token refill after wait, (c) warmup at day 0/3/7/10/14/15, (d) burst never zero at 25%, (e) concurrent Allow() with -race; use `testing/synctest` for deterministic timing (SC-003, SC-004, research D6)

### Safety pipeline

- [x] T012 Implement `SafetyPipeline` struct in `internal/app/safety.go`: `Check(jid domain.JID, action domain.Action) error` that calls `Allowlist.Allows` then `RateLimiter.Allow`; returns `ErrNotAllowlisted`, `ErrRateLimited`, or `ErrWarmupActive` (FR-009, FR-011..FR-018, contracts/dispatcher-impl.md §Safety pipeline)
- [x] T013 [P] Implement `safety_test.go` in `internal/app/`: test allowlist deny path (no rate token consumed), rate limit deny path, warmup deny path, exempt methods bypass (SC-002)

### Event bridge

- [x] T014 Implement `EventBridge` struct in `internal/app/eventbridge.go`: bridge goroutine reads `EventStream.Next(ctx)`, translates `domain.Event` to `AppEvent` per FR-033 type mapping, pushes to `Events()` channel AND registered wait waiters; retry on non-cancel errors with 100ms backoff; close channel on ctx cancel (FR-032..FR-035, research D4)
- [x] T015 [P] Implement `eventbridge_test.go` in `internal/app/`: test (a) 3 events delivered in order, (b) filter-only delivery to waiters, (c) shutdown closes channel + no goroutine leak via goleak, (d) error retry on non-cancel errors (SC-005, SC-006)

**Checkpoint**: Foundation ready. Rate limiter, safety pipeline, and event bridge all independently tested.

---

## Phase 3: US1 — Send messages through the safety stack (Priority: P1) 🎯 MVP

**Goal**: `send`, `sendMedia`, and `react` methods work end-to-end with allowlist + rate limiter + warmup + audit.

**Independent Test**: `go test -race -run TestSend ./internal/app/...` against in-memory fakes.

**Commit boundary**: `feat(005): send/sendMedia/react with safety pipeline + audit + US1 tests`

- [x] T016 [US1] Implement `AppDispatcher` struct in `internal/app/dispatcher.go`: constructor `NewAppDispatcher(opts)` accepting all 8 ports + session timestamp + logger; method table populated at construction; `Handle` routes to handlers; `Events()` returns bridge channel; `Close()` cancels ctx and waits for bridge (FR-001..FR-004, contracts/dispatcher-impl.md)
- [x] T017 [US1] Implement `handleSend` in `internal/app/method_send.go`: parse `{to, body}` params, parse JID via `domain.ParseJID`, run safety pipeline, call `MessageSender.Send` with `domain.TextMessage`, record audit, return `{messageId, timestamp}` (FR-005, FR-009, FR-010)
- [x] T018 [P] [US1] Implement `handleSendMedia` in `internal/app/method_send.go`: parse `{to, path, caption?, mime?}` params, same safety pipeline, `domain.MediaMessage` (FR-006)
- [x] T019 [P] [US1] Implement `handleReact` in `internal/app/method_send.go`: parse `{chat, messageId, emoji}` params, same safety pipeline, `domain.ReactionMessage`, return `{}` (FR-007)
- [x] T020 [US1] Wire audit logging in `method_send.go`: after safety check + port call, call `AuditLog.Record` with appropriate decision string; log audit write failures at ERROR without failing the request (FR-036, FR-037)
- [x] T021 [P] [US1] Test "send succeeds with allowlisted JID" in `internal/app/method_send_test.go`: verify messageId returned, audit entry recorded with "ok" (SC-001, SC-007)
- [x] T022 [P] [US1] Test "send denied by allowlist" in `method_send_test.go`: verify ErrNotAllowlisted, no MessageSender.Send call, audit entry with "denied:allowlist" (SC-002)
- [x] T023 [P] [US1] Test "send denied by rate limiter" in `method_send_test.go`: exhaust bucket, verify ErrRateLimited, audit with "denied:rate"
- [x] T024 [P] [US1] Test "send denied by warmup" in `method_send_test.go`: session age 3 days, exceed 25% cap, verify ErrWarmupActive
- [x] T025 [P] [US1] Test "sendMedia and react go through same pipeline" in `method_send_test.go`
- [x] T026 [P] [US1] Test "nil/empty params returns ErrInvalidParams" in `method_send_test.go`

**Checkpoint**: US1 shippable. The core send pipeline works with all safety gates.

---

## Phase 4: US2 — Device pairing (Priority: P1)

**Goal**: `pair` method works, bypasses safety pipeline, checks for existing session.

**Independent Test**: `go test -race -run TestPair ./internal/app/...`

**Commit boundary**: `feat(005): pair method + US2 tests`

- [ ] T027 [US2] Implement `handlePair` in `internal/app/method_pair.go`: parse `{phone?}` params, check `SessionStore.Load()` for existing session → ErrNotPaired (already paired), delegate to adapter pairing capability, return `{paired: true}` or `{paired: true, code: "..."}` (FR-023..FR-026)
- [ ] T028 [P] [US2] Test "pair succeeds when no session exists" in `internal/app/dispatcher_test.go`
- [ ] T029 [P] [US2] Test "pair returns already-paired when session exists" in `dispatcher_test.go`
- [ ] T030 [P] [US2] Test "pair bypasses safety pipeline" in `dispatcher_test.go`: verify allowlist and rate limiter NOT consulted

**Checkpoint**: US2 shippable. Pairing works independently.

---

## Phase 5: US3 — Stream inbound events (Priority: P2)

**Goal**: Event bridge delivers events to `Events()` channel; `wait` method blocks until matching event.

**Independent Test**: `go test -race -run TestEventBridge ./internal/app/...` and `-run TestWait`

**Commit boundary**: `feat(005): wait method + event bridge fan-out + US3 tests`

- [ ] T031 [US3] Implement `handleWait` in `internal/app/method_wait.go`: parse `{events?, timeoutMs?}` params (defaults: all, 30s), register waiter on bridge with filter, block on waiter channel with `context.WithTimeout`, return the event on success or ErrWaitTimeout (FR-029..FR-031, research D4)
- [ ] T032 [P] [US3] Test "wait returns matching event" in `internal/app/eventbridge_test.go`: push event matching filter, verify wait returns it
- [ ] T033 [P] [US3] Test "wait times out" in `eventbridge_test.go`: no event pushed, verify ErrWaitTimeout; use synctest for deterministic timeout
- [ ] T034 [P] [US3] Test "wait filters by event type" in `eventbridge_test.go`: push non-matching then matching event, verify only matching returned
- [ ] T035 [P] [US3] Test "bridge delivers to both Events() and wait waiter simultaneously" in `eventbridge_test.go`

**Checkpoint**: US3 shippable. Streaming and blocking-wait both work.

---

## Phase 6: US4 + US5 — Status/groups + warmup (Priority: P2/P3)

**Goal**: Read-only queries work without safety pipeline; warmup ramp is fully verified.

**Independent Test**: `go test -race -run 'TestStatus|TestGroups|TestWarmup' ./internal/app/...`

**Commit boundary**: `feat(005): status/groups/markRead methods + warmup verification + US4/US5 tests`

- [ ] T036 [US4] Implement `handleStatus` in `internal/app/method_status.go`: query connection state from adapter, return `{connected, jid?}` (FR-027)
- [ ] T037 [P] [US4] Implement `handleGroups` in `internal/app/method_status.go`: call `GroupManager.List()`, marshal result (FR-028)
- [ ] T038 [US4] Implement `handleMarkRead` in `internal/app/method_markread.go`: parse `{chat, messageId}`, run safety pipeline, call `MessageSender.MarkRead`, audit (FR-008, FR-009)
- [ ] T039 [P] [US4] Test "status returns connected state" in `internal/app/dispatcher_test.go`
- [ ] T040 [P] [US4] Test "groups returns group list" in `dispatcher_test.go`
- [ ] T041 [P] [US4] Test "status and groups bypass safety pipeline" in `dispatcher_test.go`
- [ ] T042 [P] [US4] Test "markRead goes through safety pipeline" in `dispatcher_test.go`
- [ ] T043 [P] [US5] Test "warmup at day 3 limits to 25% caps" in `internal/app/ratelimiter_test.go` (SC-004) — uses synctest
- [ ] T044 [P] [US5] Test "warmup at day 10 limits to 50% caps" in `ratelimiter_test.go` — uses synctest
- [ ] T045 [P] [US5] Test "warmup at day 15 gives full caps" in `ratelimiter_test.go`
- [ ] T046 [P] [US5] Test "warmup has no override mechanism" in `ratelimiter_test.go`: verify no public method to bypass

**Checkpoint**: All five user stories complete.

---

## Phase 7: Polish & cross-cutting concerns

**Purpose**: Integration test, goroutine leaks, lint, quickstart, tag.

**Commit boundary**: `chore(test): polish — integration test, goleak, depguard, quickstart` then tag

- [ ] T047 [P] Implement full-pipeline integration test in `internal/app/dispatcher_test.go`: construct AppDispatcher with all memory fakes, exercise send + pair + status + groups + wait in sequence, verify audit log has correct count (SC-007)
- [ ] T048 [P] Wire `goleak.VerifyTestMain(m)` in `internal/app/dispatcher_test.go` if not already present (SC-006)
- [ ] T049 Run `go test -race -count=3 ./internal/app/...` and verify zero leaks, zero races (SC-008)
- [ ] T050 Run `golangci-lint run ./...` — verify zero findings; confirm `app-no-adapters` depguard rule passes (SC-010, FR-042)
- [ ] T051 Walk quickstart.md steps 1-8; verify all pass within 5 minutes (SC-009)
- [ ] T052 Push branch and tag: `git push origin 005-app-usecases && git tag -a v0.0.5-app-usecases -m "feature 005: application use cases with safety pipeline"`; push tag

**Checkpoint**: Feature 005 complete. PR can be opened against `main`.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: No dependencies; extends existing ports
- **Foundational (Phase 2)**: Depends on Phase 1 (T001-T007 must complete); BLOCKS all user stories
- **US1 (Phase 3)**: Depends on Phase 2 (needs RateLimiter, SafetyPipeline, EventBridge, errors)
- **US2 (Phase 4)**: Depends on Phase 2 (needs AppDispatcher from T016 in US1, but only the struct — pair doesn't use safety pipeline). Can start after T016.
- **US3 (Phase 5)**: Depends on Phase 2 (needs EventBridge for wait method). Can start in parallel with US1 if EventBridge is done.
- **US4+US5 (Phase 6)**: Depends on Phase 2 + US1 (status/groups are simple; markRead uses safety pipeline; warmup tests validate existing ratelimiter)
- **Polish (Phase 7)**: Depends on all user stories complete

### User story dependencies

| Story | Phase | Depends on | Blocks |
|---|---|---|---|
| US1 (P1) | 3 | Phase 2 | US2 (needs dispatcher struct), US4 (markRead uses pipeline) |
| US2 (P1) | 4 | Phase 2, T016 | — |
| US3 (P2) | 5 | Phase 2 | — |
| US4+US5 (P2/P3) | 6 | Phase 2, US1 | — |

### Parallel opportunities

| Phase | Parallel tasks |
|---|---|
| 1 | T003, T004, T005, T006 (different files) |
| 2 | T009 (errors) ∥ T011 (ratelimiter test) ∥ T013 (safety test) ∥ T015 (bridge test) — after their production code |
| 3 | T018, T019 (sendMedia, react) ∥ T021-T026 (all test files) — after T016, T017 |
| 4 | T028, T029, T030 (all tests) — after T027 |
| 5 | T032-T035 (all tests) — after T031 |
| 6 | T037, T039-T046 (all tests + groups) — after T036, T038 |
| 7 | T047, T048 |

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1: Setup (extend ports, add deps) — Commit 1
2. Phase 2: Foundational (errors, rate limiter, safety, bridge) — Commit 2
3. Phase 3: US1 send pipeline — Commit 3
4. **STOP and validate**: `go test -race -run TestSend ./internal/app/...` proves the safety stack works
5. Ship as MVP — the daemon can now send messages with allowlist + rate limit + audit

### Incremental delivery

1. MVP (US1) → demo send with safety gates
2. + US2 → demo pairing through the use case layer
3. + US3 → demo `wa wait` blocking event receipt
4. + US4/US5 → demo status + groups + full warmup verification
5. Polish → tag `v0.0.5-app-usecases` → PR to merge

---

## Notes

- 52 tasks across 7 phases, 6 commit boundaries
- 5 user stories: 2 P1 (US1, US2) + 2 P2 (US3, US4) + 1 P3 (US5)
- Every test uses in-memory fakes; no whatsmeow dependency; no integration gate
- `testing/synctest` for rate-limiter/warmup tests (research D6); real timeouts not needed
- `goleak.VerifyTestMain` wired in Phase 7 (T048)
- Estimated LOC: ~1330 (production ~730, tests ~550, existing file changes ~50)
- One cross-feature change: `MarkRead` added to `MessageSender` in Phase 1 (T002-T005)
- New depguard rule `app-no-adapters` in Phase 1 (T006) mechanically enforces hexagonal boundary
