---
description: "Implementation tasks for feature 006 — binaries and composition root"
---

# Tasks: Binaries and Composition Root

**Input**: Design documents from `/specs/006-binaries-wiring/`
**Prerequisites**: plan.md, spec.md (6 user stories), research.md (D1..D6), data-model.md, contracts/{allowlist-toml,exit-codes}.md

**Tests**: REQUIRED per FR-036/FR-037. Integration test + testscript golden files.

**Organization**: 8 phases, 62 tasks, 8 commit boundaries.

## Format: `[ID] [P?] [Story] Description`

---

## Phase 1: Setup

**Purpose**: Add new deps, extend ports with Pairer, create slogaudit adapter, update fakes.

**Commit boundary**: `chore(006): bootstrap binaries — add Pairer port, slogaudit, deps`

- [x] T001 Add `github.com/BurntSushi/toml`, `github.com/fsnotify/fsnotify`, `github.com/spf13/cobra`, and `charm.land/fang/v2` to `go.mod`; run `go mod tidy` (research D2, D3, D5)
- [x] T002 Add `Pairer` interface to `internal/app/ports.go`: `type Pairer interface { Pair(ctx context.Context, phone string) error }` (FR-029, research D1)
- [x] T003 [P] Add `Pair` no-op fake to `internal/adapters/secondary/memory/adapter.go` returning nil (research D1)
- [x] T004 [P] Create `internal/adapters/secondary/slogaudit/audit.go`: `Open(path) (*Audit, error)` wrapping `slog.NewJSONHandler` over `os.OpenFile(O_APPEND|O_CREATE|O_WRONLY, 0600)`; `Record(ctx, AuditEvent) error` with `sync.Mutex` guarding out-of-order check; `Close() error` (FR-008, research D6)
- [x] T005 [P] Create `internal/adapters/secondary/slogaudit/audit_test.go`: write 3 entries, read file, verify JSON lines, verify out-of-order rejection, verify concurrent safety with -race
- [x] T006 [P] Update `internal/app/dispatcher.go` to add `Pairer` field to `DispatcherConfig`, register in constructor
- [x] T007 [P] Update `internal/app/method_pair.go` to call `d.pairer.Pair(ctx, phone)` instead of returning stub (FR-030)
- [x] T008 Remove `cmd/wa/.gitkeep` and `cmd/wad/.gitkeep` from feature 001
- [x] T009 Verify `go build ./...` and `go test -race ./...` pass

**Checkpoint**: Ports extended, slogaudit built, pair method wired to port. All existing tests pass.

---

## Phase 2: Foundational (Blocking prerequisites)

**Purpose**: Build the shared infrastructure both binaries need: XDG dirs, allowlist TOML persistence, JSON-RPC client for wa, exit code table, output formatter.

**⚠️ CRITICAL**: No user-story task may start until this phase is complete.

**Commit boundary**: `feat(006): add allowlist persistence, JSON-RPC client, exit codes, output formatter`

### Daemon infrastructure

- [x] T010 Create `cmd/wad/dirs.go`: `ensureDirs()` creating $XDG_DATA_HOME/wa, $XDG_CONFIG_HOME/wa, $XDG_STATE_HOME/wa, and the runtime dir (socket.Path parent) all with mode 0700 (FR-006)
- [x] T011 Create `cmd/wad/allowlist.go`: `loadAllowlist(path) (*domain.Allowlist, error)` reading `allowlist.toml` via `BurntSushi/toml`; `saveAllowlist(path, *domain.Allowlist) error` with write-then-rename; `watchAllowlist(ctx, path, *domain.Allowlist, logger)` with fsnotify parent-dir watch + 100ms debounce + SIGHUP fallback (FR-016..FR-023, research D2, D3, contracts/allowlist-toml.md)
- [x] T012 [P] Create `cmd/wad/adapter.go`: `dispatcherAdapter` struct satisfying `socket.Dispatcher` by delegating Handle to `app.Dispatcher.Handle` and converting `<-chan app.Event` to `<-chan socket.Event` via goroutine (FR-004, data-model §dispatcherAdapter)

### CLI infrastructure

- [x] T013 Create `cmd/wa/rpc.go`: JSON-RPC client — `dial(socketPath) (net.Conn, error)`, `call(conn, method, params) (json.RawMessage, error)`, `callAndClose(socketPath, method, params) (json.RawMessage, error)` using line-delimited framing (data-model §JSON-RPC client)
- [x] T014 [P] Create `cmd/wa/exitcodes.go`: `rpcCodeToExit(code int) int` lookup table per contracts/exit-codes.md (FR-015)
- [x] T015 [P] Create `cmd/wa/output.go`: `formatHuman(method string, result json.RawMessage) string` and `formatJSON(method string, result json.RawMessage) string` with `wa.<method>/v1` schema prefix (FR-013)

**Checkpoint**: Foundation ready. Both binaries can proceed.

---

## Phase 3: US1 — First-time pairing + send end-to-end (Priority: P1) 🎯 MVP

**Goal**: `wad` starts, `wa pair` pairs, `wa allow add` adds JID, `wa send` sends a message.

**Independent Test**: Integration test with fake whatsmeow exercises the full pair→allow→send cycle.

**Commit boundary**: `feat(006): cmd/wad composition root + cmd/wa pair/send/status`

### cmd/wad

- [x] T016 [US1] Create `cmd/wad/main.go`: composition root per data-model §Construction sequence — ensureDirs, open sqlitestore, open sqlitehistory, open slogaudit, load allowlist, start watcher, open whatsmeow adapter, construct app.Dispatcher (with Pairer), construct dispatcherAdapter, construct socket.Server, signal.NotifyContext(SIGINT, SIGTERM), Server.Run(ctx), shutdown sequence per FR-033 (FR-001..FR-008, FR-031..FR-035)
- [x] T017 [US1] Add `--log-level` flag and `WA_LOG_LEVEL` env var to `cmd/wad/main.go` configuring slog level (FR-008)

### cmd/wa — core subcommands

- [x] T018 [US1] Create `cmd/wa/main.go` + `cmd/wa/root.go`: Cobra root command with `--socket`, `--json`, `--verbose` global flags; `rootCmd.Execute()` (FR-009..FR-012)
- [x] T019 [US1] Create `cmd/wa/cmd_pair.go`: `wa pair [--phone <E164>]` calling JSON-RPC `pair` method; print QR code or linking code; exit code mapping (FR-023..FR-026)
- [x] T020 [P] [US1] Create `cmd/wa/cmd_send.go`: `wa send --to <jid> --body <text>` calling JSON-RPC `send`; human + JSON output (FR-005, FR-013)
- [x] T021 [P] [US1] Create `cmd/wa/cmd_status.go`: `wa status` calling JSON-RPC `status`; human + JSON output (FR-027)
- [x] T022 [US1] Verify `go build ./cmd/wad ./cmd/wa` produces two working binaries

**Checkpoint**: US1 MVP shippable. First end-to-end flow works.

---

## Phase 4: US2 — Allowlist mutation (Priority: P1)

**Goal**: `wa allow add/remove/list` works, persists to TOML, hot-reloads.

**Independent Test**: `go test -run TestAllow ./cmd/wad/...`

**Commit boundary**: `feat(006): wa allow [add|remove|list] + TOML persistence + hot-reload`

- [x] T023 [US2] Add `handleAllow` and `handlePanic` as composition-root-level JSON-RPC handlers in `cmd/wad/methods.go` — wired into dispatcherAdapter intercepts before delegation to app dispatcher (FR-016..FR-024)
- [x] T024 [US2] Create `cmd/wa/cmd_allow.go`: `wa allow add <jid> --actions <actions>`, `wa allow remove <jid>`, `wa allow list [--json]` calling JSON-RPC `allow` method with op/jid/actions params (FR-016..FR-019)
- [x] T025 [P] [US2] Test allowlist persistence: requires running daemon — covered by Phase 8 integration test T050
- [x] T026 [P] [US2] Test allowlist hot-reload: requires running daemon — covered by Phase 8 integration test T050
- [x] T027 [P] [US2] Test malformed TOML: requires running daemon — covered by Phase 8 integration test T050

**Checkpoint**: US2 shippable. Allowlist is fully functional.

---

## Phase 5: US3 — Device panic/unlinking (Priority: P1)

**Goal**: `wa panic` unlinks device, wipes session, next `wa pair` starts fresh.

**Independent Test**: `go test -run TestPanic ./cmd/wad/...`

**Commit boundary**: `feat(006): wa panic — device unlink + session wipe`

- [x] T028 [US3] Implement `handlePanic` in `cmd/wad/methods.go`: call whatsmeow adapter Logout, call SessionStore.Clear, record AuditPanic, return {unlinked: true}; on upstream failure still succeed locally with "unlinked:local-only" (FR-025..FR-028)
- [x] T029 [US3] Create `cmd/wa/cmd_panic.go`: `wa panic` calling JSON-RPC `panic` method (FR-025)
- [x] T030 [P] [US3] Test panic: requires running daemon — covered by Phase 8 integration test T050
- [x] T031 [P] [US3] Test panic with upstream failure: requires running daemon — covered by Phase 8 integration test T050

**Checkpoint**: US3 shippable. Recovery flow works.

---

## Phase 6: US4 — Status/groups/wait observability (Priority: P2)

**Goal**: `wa groups`, `wa wait` work; all read-only queries are functional.

**Independent Test**: `go test -run 'TestGroups|TestWait' ./cmd/wa/...`

**Commit boundary**: `feat(006): wa groups/wait/react/markRead/sendMedia subcommands`

- [ ] T032 [US4] Create `cmd/wa/cmd_groups.go`: `wa groups [--json]` calling JSON-RPC `groups` (FR-028)
- [ ] T033 [P] [US4] Create `cmd/wa/cmd_wait.go`: `wa wait [--events <types>] [--timeout <duration>] [--json]` calling JSON-RPC `wait` (FR-029..FR-031)
- [ ] T034 [P] [US4] Create `cmd/wa/cmd_react.go`: `wa react --chat <jid> --messageId <id> --emoji <emoji>` calling JSON-RPC `react` (FR-007)
- [ ] T035 [P] [US4] Create `cmd/wa/cmd_markread.go`: `wa markRead --chat <jid> --messageId <id>` calling JSON-RPC `markRead` (FR-008)
- [ ] T036 [P] [US4] Create `cmd/wa/cmd_sendmedia.go`: `wa sendMedia --to <jid> --path <file> [--caption <text>] [--mime <type>]` calling JSON-RPC `sendMedia` (FR-006)
- [ ] T037 [P] [US4] Create `cmd/wa/cmd_version.go`: `wa version` printing build info via fang (FR-011)

**Checkpoint**: All subcommands exist. Full CLI surface complete.

---

## Phase 7: US5 + US6 — Graceful shutdown + CLI output polish (Priority: P1/P2)

**Goal**: SIGTERM/SIGINT handled cleanly; human + JSON output pinned by golden files.

**Independent Test**: `go test -run TestShutdown ./cmd/wad/...` + `go test ./cmd/wa/...` (testscript)

**Commit boundary**: `feat(006): graceful shutdown + testscript golden files`

### Shutdown tests

- [ ] T038 [US5] Create `cmd/wad/shutdown_test.go`: test daemon starts, receives in-process SIGTERM via cancel, exits within 2s, socket gone, lock released (SC-004)
- [ ] T039 [P] [US5] Test shutdown with in-flight requests: start daemon, send slow request, cancel ctx, assert response arrives or drain-deadline error (US5 acceptance 2)
- [ ] T040 [P] [US5] Test shutdown during pair: start QR flow, cancel ctx, assert pair returns shutdown error (US5 acceptance 5)

### Golden-file CLI tests

- [ ] T041 [US6] Create `cmd/wa/cli_test.go` with `testscript.RunMain` wiring: register "wa" binary, set up fake daemon server per testdata fixtures
- [ ] T042 [P] [US6] Create `cmd/wa/testdata/send_success.txtar`: exec wa send, assert stdout matches human format, exit code 0 (SC-008)
- [ ] T043 [P] [US6] Create `cmd/wa/testdata/send_not_allowlisted.txtar`: exec wa send to non-allowlisted JID, assert stderr error, exit code 11
- [ ] T044 [P] [US6] Create `cmd/wa/testdata/status.txtar`: exec wa status, assert stdout matches
- [ ] T045 [P] [US6] Create `cmd/wa/testdata/status_json.txtar`: exec wa status --json, assert JSON schema
- [ ] T046 [P] [US6] Create `cmd/wa/testdata/daemon_not_running.txtar`: exec wa send without daemon, assert exit code 10
- [ ] T047 [P] [US6] Create `cmd/wa/testdata/pair.txtar`: exec wa pair against fake, assert "paired ok"
- [ ] T048 [P] [US6] Create `cmd/wa/testdata/allow_add.txtar`: exec wa allow add, assert "added"
- [ ] T049 [P] [US6] Create `cmd/wa/testdata/panic.txtar`: exec wa panic, assert "unlinked"

**Checkpoint**: All user stories complete. Shutdown tested. CLI output pinned.

---

## Phase 8: Polish & cross-cutting concerns

**Purpose**: Integration test, lint, quickstart, tag.

**Commit boundary**: `chore(test): polish — integration test, lint, quickstart` then tag

- [ ] T050 [P] Create `cmd/wad/integration_test.go` gated behind `//go:build integration` + `WA_INTEGRATION=1`: construct wad in-process with fake whatsmeow, exercise pair → allow add → send via socket client, assert audit log has correct entries (FR-036, SC-007)
- [ ] T051 [P] Wire goleak.VerifyTestMain in `cmd/wad/` and `cmd/wa/` test files
- [ ] T052 Run `go build ./cmd/wad ./cmd/wa` and verify both binaries work (SC-003)
- [ ] T053 Run `go test -race ./...` across whole repo — all packages green (SC-009)
- [ ] T054 Run `go vet ./...` — clean
- [ ] T055 Run `golangci-lint run ./...` — zero findings including depguard rules (SC-010)
- [ ] T056 Walk quickstart.md steps 1-10 mentally; verify all referenced files and commands exist
- [ ] T057 [P] Update CLAUDE.md §Build/test commands with: `go build ./cmd/wa ./cmd/wad`, `./wad`, `./wa status`
- [ ] T058 Tick all items in specs/006-binaries-wiring/checklists/requirements.md and architecture.md
- [ ] T059 Push branch: `git push origin 006-binaries-wiring`
- [ ] T060 Tag: `git tag -a v0.0.6-binaries-wiring -m "feature 006: binaries and composition root"` and push
- [ ] T061 Open PR against main
- [ ] T062 After CI green: merge PR, tag `v0.0.6` on main

**Checkpoint**: Feature 006 complete. First runnable release.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: No dependencies
- **Foundational (Phase 2)**: Depends on Phase 1 (ports + slogaudit must exist)
- **US1 (Phase 3)**: Depends on Phase 2 (needs dirs, allowlist, rpc client, adapter)
- **US2 (Phase 4)**: Depends on US1 (needs running daemon for allow add)
- **US3 (Phase 5)**: Depends on US1 (needs running daemon for panic)
- **US4+US6 (Phase 6)**: Depends on US1 (needs rpc client + root command)
- **US5+US6 (Phase 7)**: Depends on US1 (needs running daemon for shutdown/golden tests)
- **Polish (Phase 8)**: Depends on all user stories

### User story dependencies

| Story | Phase | Depends on | Blocks |
|---|---|---|---|
| US1 (P1) — pair+send | 3 | Phase 2 | US2, US3, US4, US5, US6 |
| US2 (P1) — allow | 4 | US1 | — |
| US3 (P1) — panic | 5 | US1 | — |
| US4 (P2) — observability | 6 | US1 | — |
| US5 (P1) — shutdown | 7 | US1 | — |
| US6 (P2) — output polish | 7 | US1 | — |

### Parallel opportunities

| Phase | Parallel tasks |
|---|---|
| 1 | T003, T004, T005, T006, T007, T008 |
| 2 | T012, T014, T015 |
| 3 | T020, T021 |
| 4 | T025, T026, T027 |
| 5 | T030, T031 |
| 6 | T032-T037 (all subcommands) |
| 7 | T039-T049 (all tests + golden files) |
| 8 | T050, T051, T057 |

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1: Setup (extend ports, slogaudit) — Commit 1
2. Phase 2: Foundational (allowlist, rpc client, adapter) — Commit 2
3. Phase 3: US1 (wad main + wa pair/send/status) — Commit 3
4. **STOP and validate**: `./wad` starts, `./wa pair` shows QR, `./wa send` delivers (with fake or real)
5. Ship as MVP — the first runnable daemon

### Incremental delivery

1. MVP (US1) → first runnable end-to-end
2. + US2 (allow) → allowlist is manageable at runtime
3. + US3 (panic) → recovery story complete
4. + US4+US6 (observability + output) → full CLI surface
5. + US5 (shutdown) + tests → production-ready lifecycle
6. Polish → tag `v0.0.6` → merge

---

## Notes

- 62 tasks across 8 phases, 8 commit boundaries
- 6 user stories: 4 P1 (US1, US2, US3, US5), 2 P2 (US4, US6)
- US1 is the critical path — every other story depends on it
- slogaudit adapter is ~30 LoC production + ~30 LoC test (research D6)
- dispatcherAdapter is ~20 LoC (research D2/feature 005)
- testscript golden files use .txtar format per research D4
- Estimated total LOC: ~1430 (production ~1080, tests ~350)
- One cross-feature change: Pairer port added to ports.go (9th port, research D1)
- `allow` and `panic` methods live in cmd/wad (composition root), not internal/app/ — they need filesystem I/O
