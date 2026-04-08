---
description: "Implementation tasks for feature 004 — socket primary adapter"
---

# Tasks: Socket Primary Adapter

**Input**: Design documents from `/specs/004-socket-adapter/`
**Prerequisites**: plan.md, spec.md (5 user stories), research.md (D1..D13), data-model.md, contracts/{wire-protocol,dispatcher,socket-path}.md

**Tests**: REQUIRED. The contract test suite at `internal/adapters/primary/socket/sockettest/` is itself a primary deliverable per FR-042 and the test-coverage requirement at the end of every contract document.

**Organization**: 8 phases, 68 tasks, 9 commit boundaries aligned with the plan in plan.md.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks in the same phase)
- **[Story]**: Which user story this task belongs to (US1..US5 from spec.md)

## Path conventions

All paths are absolute under `/Users/notroot/Documents/Code/WhatsAppAutomation/`. Production code lives under `internal/adapters/primary/socket/`; the contract test suite lives under `internal/adapters/primary/socket/sockettest/`. OS-specific files use Go build tags rather than runtime switches.

---

## Phase 1: Setup (Shared infrastructure)

**Purpose**: Add dependencies, create empty package skeletons, harden the depguard rule.

**Commit boundary**: end of phase = `chore(004): bootstrap socket adapter package + deps`

- [X] T001 Add `github.com/creachadair/jrpc2` (production) and `go.uber.org/goleak` (test only) to `go.mod`; run `go mod tidy`; verify both modules appear in `go.sum`
- [X] T002 Create production package skeleton: `internal/adapters/primary/socket/doc.go` with package documentation declaring "transport layer, no business logic"; remove `internal/adapters/primary/socket/.gitkeep` from feature 001
- [X] T003 [P] Create test package skeleton: `internal/adapters/primary/socket/sockettest/doc.go` with package documentation declaring it as a reusable contract suite consumable by future primary adapters
- [X] T004 [P] Extend `.golangci.yml` `depguard` rules with a new rule `adapters-primary-no-whatsmeow` forbidding `go.mau.fi/whatsmeow/*` imports under `internal/adapters/primary/**`; verify the existing `core-no-whatsmeow` rule still passes via `golangci-lint run`

**Checkpoint**: package compiles empty; depguard rules in place.

---

## Phase 2: Foundational (Blocking prerequisites)

**Purpose**: Build the seam interface, error code table, path resolver, lock, peer-cred check, connection skeleton, and the contract test scaffolding. None of the user stories can begin until this phase is complete.

**⚠️ CRITICAL**: No user-story task may start until every task here is checked off.

**Commit boundaries**: 
- T005..T009 → `feat(004): add Dispatcher interface, error codes, OS path resolver`
- T010..T013 → `feat(004): add lockedfile single-instance gate + peer-cred syscalls`
- T014..T019 → `feat(004): add Connection skeleton + sockettest scaffolding + goleak gate`

**Note**: Contract files `contracts/wire-protocol.md`, `contracts/dispatcher.md`, and `contracts/socket-path.md` already exist from `/speckit:plan` — they satisfy FR-039, FR-040, FR-041 before implementation begins (SC-007).

### Interface, error codes, paths

- [X] T005 Define the `Dispatcher` interface and `Event` struct in `internal/adapters/primary/socket/dispatcher.go` per contracts/dispatcher.md (`Handle(ctx, method string, params json.RawMessage) (json.RawMessage, error)` and `Events() <-chan Event`)
- [X] T006 [P] Implement the JSON-RPC error code constants and `ErrorCode` type in `internal/adapters/primary/socket/errcodes.go` per contracts/wire-protocol.md error code table; assert at compile time that no constant lies in the `-32011..-32099` reserved-for-future block
- [X] T007 [P] Implement sentinel error types (`ErrAlreadyRunning`, `ErrInvalidPath`, `ErrPathTooLong`, `ErrParentCreate`, `ErrParentWorldWritable`, `ErrParentSymlinkAttack`, `ErrListen`, `ErrChmod`, `ErrBackpressure`, `ErrShutdown`) in `internal/adapters/primary/socket/errors.go`
- [X] T008 [P] Implement Linux path resolver in `internal/adapters/primary/socket/path_linux.go` (build tag `//go:build linux`) returning `filepath.Join(xdg.RuntimeDir, "wa", "wa.sock")` (FR-001)
- [X] T009 [P] Implement darwin path resolver in `internal/adapters/primary/socket/path_darwin.go` (build tag `//go:build darwin`) returning `filepath.Join(home, "Library", "Caches", "wa", "wa.sock")` via `os.UserHomeDir()`; explicitly do NOT call `xdg.RuntimeDir` per research.md §Contradicts blueprint (FR-001)

### Lock and peer credentials

- [X] T010 Implement listener pre-flight checks (`sun_path` length, parent dir existence + mode 0700, world-writable rejection, symlink-in-parent rejection, post-create chmod 0600 + verify) in `internal/adapters/primary/socket/listener.go` per contracts/socket-path.md §Pre-flight checks (FR-002, FR-003, FR-005)
- [X] T011 [P] Implement single-instance lock using `lockedfile.Mutex` on a sibling `<socket>.lock` file in `internal/adapters/primary/socket/lock.go` per research.md D8; expose `Acquire(path string) (release func(), err error)` (FR-016, FR-017, FR-018, FR-019)
- [X] T012 [P] Implement Linux peer-credential check via `unix.GetsockoptUcred(fd, SOL_SOCKET, SO_PEERCRED)` in `internal/adapters/primary/socket/peercred_linux.go` (build tag `//go:build linux`); access `fd` via `(*net.UnixConn).SyscallConn().Control` (FR-013, FR-014, FR-015)
- [X] T013 [P] Implement darwin peer-credential check via `unix.GetsockoptXucred(fd, SOL_LOCAL, LOCAL_PEERCRED)` in `internal/adapters/primary/socket/peercred_darwin.go` (build tag `//go:build darwin`) (FR-013, FR-014, FR-015)

### Connection skeleton + test scaffolding

- [X] T014 Implement the `Connection` struct (id, peerUID, raw conn, log, ctx/cancel, subscriptions map, out mailbox channel, inFlight atomic, mu, createdAt) in `internal/adapters/primary/socket/connection.go` per data-model.md; do NOT wire jrpc2 yet — that comes in US1 (FR-027, FR-029, FR-030)
- [X] T015 [P] Implement `FakeDispatcher` in `internal/adapters/primary/socket/sockettest/fake_dispatcher.go` per contracts/dispatcher.md §Fake dispatcher: `On(method, fn)`, `PushEvent(Event)`, `Close()`, `Calls()`
- [X] T016 [P] Implement test helpers in `internal/adapters/primary/socket/sockettest/helpers.go`: temp socket-path generator, line-delimited JSON sender + receiver, in-process server start/stop helpers
- [X] T017 [P] Implement contract suite entry point in `internal/adapters/primary/socket/sockettest/suite.go` exposing `RunSuite(t, newServer func(Dispatcher) *Server)` so the suite can be reused against any future primary adapter
- [X] T018 [P] Wire `goleak.VerifyTestMain(m)` in `internal/adapters/primary/socket/sockettest/leak_test.go` and an analogous `TestMain` in `internal/adapters/primary/socket/server_test.go`
- [X] T019 Verify `go build ./internal/adapters/primary/socket/...` is clean and that depguard passes against the foundational files

**Checkpoint**: Foundation ready. All user-story phases can now begin.

---

## Phase 3: US1 — Reliable request/response transport (Priority: P1) 🎯 MVP

**Goal**: A client speaking line-delimited JSON-RPC 2.0 can call any method registered on the `Dispatcher` and get a response back, including the four error paths (parse, invalid request, method not found, internal error).

**Independent Test**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run RequestResponse` passes against `FakeDispatcher` with an `echo` handler; all four error scenarios produce the documented JSON-RPC error codes.

**Commit boundary**: `feat(004): wire jrpc2 dispatch + accept loop + US1 contract tests`

### Production code

- [X] T020 [US1] Implement the `Server` struct in `internal/adapters/primary/socket/server.go`: fields per data-model.md (path, listener, lockUnlock, dispatcher, log, ctx, cancel, wg, connCounter, deadlines, caps); constructor `NewServer(dispatcher Dispatcher, opts ...ServerOption)`
- [X] T021 [US1] Wire `jrpc2.NewServer` with `channel.Line(conn, conn)` and a `handler.Map` whose default branch invokes the injected `Dispatcher.Handle` in `internal/adapters/primary/socket/dispatch.go`; configure `Server.Concurrency` to enforce the per-connection in-flight cap (FR-004, FR-006, FR-007, FR-028)
- [X] T022 [US1] Implement the accept loop in `internal/adapters/primary/socket/accept.go`: pull the peer uid via T012/T013, build the per-connection logger (FR-036, FR-037, FR-038), spawn the connection goroutine, increment `wg`
- [X] T023 [US1] Implement typed-error → JSON-RPC error translation in `dispatch.go` using the table from `errcodes.go`; recover from panics in dispatcher invocations and emit `-32603 Internal error` (FR-011, FR-012)

### Contract tests for US1

- [X] T024 [P] [US1] Contract test "echo request roundtrip" in `internal/adapters/primary/socket/sockettest/request_response_test.go`: register an `echo` handler on `FakeDispatcher`, send a request, assert exact response shape
- [X] T025 [P] [US1] Contract test "method not found returns -32601 with method name in data" in `request_response_test.go` (FR-008)
- [X] T026 [P] [US1] Contract test "parse error returns -32700 and connection stays open" in `request_response_test.go` (FR-010)
- [X] T027 [P] [US1] Contract test "invalid envelope (missing method, missing jsonrpc, wrong id type) returns -32600" in `request_response_test.go` (FR-009)
- [X] T028 [P] [US1] Contract test "typed dispatcher error mapped to documented JSON-RPC code" in `request_response_test.go` (FR-011)
- [X] T029 [P] [US1] Contract test "panic in dispatcher recovers and surfaces -32603" in `request_response_test.go`; verify the accept loop and other connections are unaffected (FR-012)

**Checkpoint**: US1 fully shippable. The MVP works against a fake dispatcher; an actual `wa` client could call methods if the dispatcher were real.

---

## Phase 4: US2 — Same-user-only socket security (Priority: P1)

**Goal**: A connection from a different uid is rejected before any bytes are read; socket file and parent directory permissions are exactly as specified; the symlink-attack and path-too-long edges are covered.

**Independent Test**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run PeerCred` passes; rejection happens at accept time without invoking `Dispatcher.Handle`.

**Commit boundary**: end of US2 + US4 → `test(004): contract tests for peer-cred and single-instance`

- [ ] T030 [P] [US2] Contract test "peer uid mismatch closes connection before any read" in `internal/adapters/primary/socket/sockettest/peercred_test.go`; mock the peer-uid getter to return a different uid; assert connection closed within 50ms (FR-013, FR-014, SC-002)
- [ ] T031 [P] [US2] Contract test "matching peer uid is admitted" in `peercred_test.go` (FR-013)
- [ ] T032 [P] [US2] Contract test "socket file mode is exactly 0600 immediately after creation" in `peercred_test.go` via `os.Stat` (FR-003)
- [ ] T033 [P] [US2] Contract test "parent directory mode is exactly 0700" in `peercred_test.go` (FR-002)
- [ ] T034 [P] [US2] Contract test "symlink in parent dir owned by other uid is refused" in `peercred_test.go`; skip with `t.Skip` on CI runners that cannot create symlinks (must still compile)
- [ ] T035 [P] [US2] Contract test "path exceeding sun_path limit returns ErrPathTooLong" in `peercred_test.go`
- [ ] T036 [P] [US2] Contract test "world-writable parent dir returns ErrParentWorldWritable" in `peercred_test.go`

**Checkpoint**: US2 shippable. Multi-user security is verified.

---

## Phase 5: US4 — Single-instance daemon guarantee (Priority: P1)

**Goal**: Only one daemon instance can run against a given socket path at a time. Stale sockets from crashed predecessors are recoverable. The lock survives unclean kill.

**Independent Test**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run SingleInstance` passes; second-server startup fails fast within 500ms.

**Commit boundary**: shared with US2 → `test(004): contract tests for peer-cred and single-instance`

- [ ] T037 [P] [US4] Contract test "second server returns ErrAlreadyRunning within 500ms" in `internal/adapters/primary/socket/sockettest/single_instance_test.go` (FR-017, SC-003)
- [ ] T038 [P] [US4] Contract test "stale socket file with released lock is unlinked and replaced" in `single_instance_test.go` (FR-018)
- [ ] T039 [P] [US4] Contract test "stale socket file with held lock is NOT touched" in `single_instance_test.go` (FR-016)
- [ ] T040 [P] [US4] Contract test "lock released on graceful shutdown allows immediate restart" in `single_instance_test.go` (FR-019)

**Checkpoint**: US4 shippable. Single-instance guarantee verified end to end.

---

## Phase 6: US3 — Streaming subscriptions (Priority: P2)

**Goal**: Clients can `subscribe` to event types and receive server-initiated notifications; back-pressure on a stalled subscriber closes the connection cleanly without leaking goroutines.

**Independent Test**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run Subscribe` passes; events are filtered by type, backpressure close fires within 1s of buffer fill, no goroutine leak after connection close.

**Commit boundary**: `feat(004): subscriptions + bounded outbound mailbox + US3 contract tests`

### Production code

- [ ] T041 [US3] Implement subscribe / unsubscribe handlers + per-connection filter table in `internal/adapters/primary/socket/subscribe.go`; mint subscription ids via `crypto/rand`-backed UUID; reject subscribe with non-string events array as `-32602 Invalid params` (FR-020, FR-021, FR-023, FR-026)
- [ ] T042 [US3] Implement bounded outbound mailbox (`chan []byte` cap 1024) and writer goroutine in `internal/adapters/primary/socket/connection.go` per research.md D10; non-blocking send via `select { case … : default: ErrBackpressure }`; on backpressure write final `-32001` frame and close (FR-024, FR-025)
- [ ] T043 [US3] Implement event fan-out goroutine in `internal/adapters/primary/socket/server.go` that reads from `Dispatcher.Events()`, filters per subscription, marshals notification frame with `schema: wa.event/v1`, and offers via the per-connection mailbox; on `Events()` channel close, emit `-32005 SubscriptionClosed` to each subscribing connection (FR-022, FR-023)

### Contract tests for US3

- [ ] T044 [P] [US3] Contract test "subscribe returns subscriptionId + schema, then receives matching events" in `internal/adapters/primary/socket/sockettest/subscribe_test.go`
- [ ] T045 [P] [US3] Contract test "events whose type is NOT in the subscription filter are not delivered" in `subscribe_test.go`
- [ ] T046 [P] [US3] Contract test "every notification frame carries `schema: wa.event/v1` and the subscription's id" in `subscribe_test.go`
- [ ] T047 [P] [US3] Contract test "stalled subscriber gets backpressure close after buffer fills" in `subscribe_test.go`; use `testing/synctest` per research.md D12 so the timing is deterministic (FR-024, SC-006)
- [ ] T048 [P] [US3] Contract test "unsubscribe releases the filter; subsequent matching events are not delivered" in `subscribe_test.go` (FR-026)
- [ ] T049 [P] [US3] Contract test "connection close releases all subscriptions within 100ms with no goroutine leak" in `subscribe_test.go` (FR-025, FR-030)
- [ ] T050 [P] [US3] Contract test "Dispatcher closing Events channel triggers final SubscriptionClosed frame" in `subscribe_test.go`

**Checkpoint**: US3 shippable. `wa wait`-style streaming works end to end against the fake dispatcher.

---

## Phase 7: US5 — Graceful shutdown (Priority: P2)

**Goal**: Cancellation drains in-flight requests within a deadline, sends shutdown notifications to active subscribers, unlinks the socket, and releases the lock.

**Independent Test**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run Shutdown` passes; `synctest`-driven deadline tests are deterministic.

**Commit boundary**: `feat(004): graceful shutdown + lifecycle tests`

### Production code

- [ ] T051 [US5] Implement `Run(ctx)`, `Shutdown()`, and `Wait()` in `internal/adapters/primary/socket/lifecycle.go` per research.md D5; cancel root ctx → close listener → `wg.Wait` under `time.After(shutdownDeadline)` → cancel surviving conn ctxs → unlink socket → release lock (FR-031, FR-032, FR-035)
- [ ] T052 [US5] Emit final `-32002 ShutdownInProgress` (for new requests during drain) and `-32003 RequestTimeoutDuringShutdown` (for in-flight requests cancelled at the deadline) frames in `lifecycle.go`; emit final shutdown frame to every active subscription (FR-033)
- [ ] T053 [US5] Implement post-shutdown cleanup in `lifecycle.go`: `os.Remove(socketPath)` ignoring ENOENT, then call the lockedfile unlock function; never remove the `.lock` sibling (FR-034)

### Contract tests for US5

- [ ] T054 [P] [US5] Contract test "clean shutdown with no in-flight requests completes within 2s of cancel" in `internal/adapters/primary/socket/sockettest/shutdown_test.go`; uses `synctest` (FR-031, SC-005)
- [ ] T055 [P] [US5] Contract test "10 in-flight requests against a slow dispatcher all receive responses before drain completes" in `shutdown_test.go`; uses `synctest` (FR-032)
- [ ] T056 [P] [US5] Contract test "in-flight request still running past drain deadline is cancelled and gets `-32003`" in `shutdown_test.go`; uses `synctest` (FR-032, SC-005)
- [ ] T057 [P] [US5] Contract test "active subscription receives final `-32002` frame before connection close" in `shutdown_test.go` (FR-033)
- [ ] T058 [P] [US5] Contract test "socket file is unlinked after shutdown completes" in `shutdown_test.go` (FR-034)
- [ ] T059 [P] [US5] Contract test "second server can start on the same path immediately after first shuts down" in `shutdown_test.go` (FR-034, FR-019)

**Checkpoint**: US5 shippable. All five user stories complete; feature is functionally done.

---

## Phase 8: Polish & cross-cutting concerns

**Purpose**: Benchmarks, leak verification, doc scripts, lint cleanup, quickstart walk, tag.

**Commit boundary**: `chore(test): polish — benchmarks, leak gate, quickstart` then `chore(release): tag v0.0.4-socket-adapter`

- [ ] T060 [P] Implement roundtrip benchmark `BenchmarkRoundtrip` in `internal/adapters/primary/socket/bench_test.go`; measure 1000 sequential request/response cycles, assert RSS growth ≤10 MiB (SC-001, SC-004)
- [ ] T061 [P] Verify `go test -race -count=3 ./internal/adapters/primary/socket/...` reports zero leaked goroutines via `goleak` (SC-008)
- [ ] T062 [P] Implement `specs/004-socket-adapter/scripts/verify-wire-protocol.sh` that parses `contracts/wire-protocol.md` for error code rows and greps `internal/adapters/primary/socket/errcodes.go` for matching constants; fails CI on missing entries either way (SC-007, FR-039)
- [ ] T063 [P] Walk `quickstart.md` steps 1-10 manually on a developer machine; capture the wall-clock time and confirm it is under 5 minutes; record the result in the checklist (SC-009)
- [ ] T064 [P] Update `CLAUDE.md` if any architectural detail changed during implementation (port count, socket path, etc.); otherwise no edit (FR-040)
- [ ] T065 Run `golangci-lint run ./...` and ensure clean output (no new findings, no regressions in existing rules) (SC-010)
- [ ] T066 Run `go test -race ./...` across the whole repo and ensure clean output; verify no test in feature 004 imports `go.mau.fi/whatsmeow/...` (SC-008, SC-010)
- [ ] T067 Tick all 39 items in `specs/004-socket-adapter/checklists/requirements.md` based on the implementation
- [ ] T068 Push the branch (`git push origin 004-socket-adapter`) and create the annotated tag `v0.0.4-socket-adapter`

**Checkpoint**: feature 004 complete. PR can be opened against `main`.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: no dependencies; can start immediately
- **Foundational (Phase 2)**: depends on Setup; BLOCKS all user stories. Within Phase 2, T005 must precede T015 (FakeDispatcher consumes the interface), T006 must precede T023 (error mapping consumes the codes), T010 must precede T019 (build verification)
- **US1 (Phase 3)**: depends on Phase 2 complete (Server needs Connection, Dispatcher, error codes, listener pre-flight, lock, peer-cred all in place)
- **US2 (Phase 4)**: depends on Phase 2 complete; pure tests, can start in parallel with US1, US4
- **US4 (Phase 5)**: depends on Phase 2 complete; pure tests, can start in parallel with US1, US2
- **US3 (Phase 6)**: depends on Phase 2 + US1 (subscription path uses the dispatch wiring from US1)
- **US5 (Phase 7)**: depends on Phase 2 + US1; shutdown drains requests so it needs the request pipeline working. Can be developed in parallel with US3 once US1 is done
- **Polish (Phase 8)**: depends on all user stories complete

### User story dependencies

| Story | Phase | Depends on | Blocks |
|---|---|---|---|
| US1 (P1) | 3 | Phase 2 | US3, US5 |
| US2 (P1) | 4 | Phase 2 | — |
| US4 (P1) | 5 | Phase 2 | — |
| US3 (P2) | 6 | Phase 2, US1 | — |
| US5 (P2) | 7 | Phase 2, US1 | — |

US2 and US4 are pure test phases — the production code that satisfies them lives in Phase 2 (peer-cred check in T012/T013, single-instance lock in T011). Their `[US2]` / `[US4]` tagging exists so traceability from spec → tasks → tests is preserved for the user stories that motivated those production tasks.

### Within each phase

- Tests and production code in the same phase: **production code first**, then tests (the spec calls for the contract suite to verify the production behavior; writing tests against missing code would be duplicated work)
- Models / interfaces / constants before consumers
- Files marked `[P]` can be edited concurrently by different developers (or different sub-agents)

### Parallel opportunities

| Phase | Parallel-eligible task IDs |
|---|---|
| 1 | T003, T004 |
| 2 | T006/T007/T008/T009 (after T005); T011/T012/T013 (after T010); T015/T016/T017/T018 (after T014) |
| 3 | T024/T025/T026/T027/T028/T029 (after T020/T021/T022/T023) |
| 4 | T030..T036 (all parallel) |
| 5 | T037..T040 (all parallel) |
| 6 | T044/T045/T046/T047/T048/T049/T050 (after T041/T042/T043) |
| 7 | T054/T055/T056/T057/T058/T059 (after T051/T052/T053) |
| 8 | T060/T061/T062/T063/T064 (all parallel before T065/T066/T067/T068) |

### Cross-phase parallelism (multi-developer)

After Phase 2 completes:
- Developer A: Phase 3 (US1)
- Developer B: Phase 4 (US2) and Phase 5 (US4) — pure tests, no production code conflicts with A
- Once A finishes Phase 3:
  - Developer A: Phase 6 (US3)
  - Developer C (or A): Phase 7 (US5)

For a single-developer flow, the natural order is `Phase 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8` matching commit boundaries.

---

## Parallel Example: Phase 2 (Foundational)

```bash
# Step A: define the seam first (single-threaded)
Task: T005 — Dispatcher interface in dispatcher.go

# Step B: now four files can be authored in parallel (no shared file)
Task: T006 — error code constants in errcodes.go
Task: T007 — sentinel errors in errors.go
Task: T008 — Linux path resolver in path_linux.go
Task: T009 — darwin path resolver in path_darwin.go

# Step C: lock + peer-cred can also be authored in parallel
Task: T010 — listener pre-flight in listener.go
Task: T011 — single-instance lock in lock.go
Task: T012 — Linux peer-cred in peercred_linux.go
Task: T013 — darwin peer-cred in peercred_darwin.go

# Step D: connection skeleton + test scaffolding (after T005..T013)
Task: T014 — Connection struct in connection.go
Task: T015 — FakeDispatcher in sockettest/fake_dispatcher.go
Task: T016 — test helpers in sockettest/helpers.go
Task: T017 — suite entry point in sockettest/suite.go
Task: T018 — goleak TestMain wiring
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1: Setup (Commit 1)
2. Phase 2: Foundational (Commits 2 + 3 + 4)
3. Phase 3: US1 — request/response (Commit 5)
4. **STOP and validate**: `go test -race ./internal/adapters/primary/socket/sockettest/ -run RequestResponse` against the fake dispatcher demonstrates that any future use case can be reached over the wire
5. Open a PR; the MVP is mergeable on its own

### Incremental delivery

1. MVP (US1) → ship → demo basic JSON-RPC roundtrip
2. + US2 + US4 (Commit 6) → ship → demo same-user security and single-instance enforcement
3. + US3 (Commit 7) → ship → demo `wa wait`-style event subscription
4. + US5 (Commit 8) → ship → demo clean shutdown
5. Polish (Commit 9) → tag `v0.0.4-socket-adapter` → open PR to merge into main

Each commit is independently testable and reverts cleanly.

### Single-developer flow (likely)

Walk Phases 1 → 8 in order, one commit per phase boundary (or two for Phase 2, which is large). Total 9 commits matching the plan.md commit boundaries.

---

## Notes

- 68 tasks across 8 phases, 9 commit boundaries
- 5 user stories: 3 P1 (US1, US2, US4) + 2 P2 (US3, US5)
- US2 and US4 are pure test phases — production code lives in Phase 2 (T011, T012, T013)
- Every test in this feature uses `FakeDispatcher`; no real WhatsApp involvement
- `testing/synctest` for every deadline-sensitive test (US3 backpressure, US5 shutdown drain)
- `goleak.VerifyTestMain` is wired in Phase 2 (T018) so it's enforced from the very first user-story test forward
- Estimated total LOC: ~2430 (matches data-model.md §LOC budget); production ~1270, tests ~1160
- Estimated wall time on a single developer: 3-5 days end to end depending on contract test depth
- Commit after each task or each `[P]` group. Stop at any phase checkpoint to validate independently. Avoid editing files outside the task's stated path.
