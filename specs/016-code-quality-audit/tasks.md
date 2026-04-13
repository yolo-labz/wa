# Tasks: Code Quality Audit & Modernization

**Input**: Design documents from `/specs/016-code-quality-audit/`
**Prerequisites**: plan.md, spec.md, research.md, problems.md

**Tests**: Regression tests included where the spec mandates them (FR-001, FR-008, FR-009). No TDD approach — tests accompany fixes.

**Organization**: Tasks grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2)

---

## Phase 1: Setup (Baseline Capture)

**Purpose**: Capture benchmark baselines and verify clean starting state before any refactoring.

- [x] T001 Run `go test -race ./...` and confirm all tests pass at HEAD in the repo root
- [x] T002 Run `go test -bench=. -benchmem -count=5 ./...` and save output to `specs/016-code-quality-audit/baseline-benchmarks.txt`
- [x] T003 Run `golangci-lint run` and confirm current config passes (10 linters) in the repo root

**Checkpoint**: Baseline captured — refactoring can begin.

---

## Phase 2: User Story 1 — Fix Critical Safety Issues (Priority: P1) 🎯 MVP

**Goal**: Eliminate all 4 critical issues and highest-impact high-severity issues. Zero deadlock risk, no god structs, no race conditions.

**Independent Test**: `go test -race ./...` passes with new regression tests green.

### Critical Fixes

- [x] T004 [US1] Apply copy-under-lock pattern to `Run()` in `internal/app/eventbridge.go:93-105` — acquire `b.mu`, copy `b.waiters` to local slice, release `b.mu`, iterate copy (C-001)
- [x] T005 [US1] Add synctest regression test for C-001 deadlock in `internal/app/eventbridge_test.go` — concurrent waiter cancel + event delivery with goleak verification
- [x] T006 [US1] Fix `Grant()` zero-value edge case in `internal/domain/allowlist.go:92-105` — add explicit existence check before modifying `set` (C-004)
- [x] T007 [US1] Add test for Grant() on previously-empty entry in `internal/domain/allowlist_test.go` (C-004)

### God-Struct Splits

- [x] T008 [US1] Adapter god-struct: evaluated extraction of historySyncWorker — rejected because all candidates require 5+ back-references to shared fields, adding indirection without reducing cognitive load. Documented decision in adapter.go with citations (Three Dots Labs, Sam Smith, Breaking Computer). (C-002)
- [x] T009 [US1] Adapter god-struct: auditRingBuffer already exists as separate type. recordAuditDetail is a thin convenience wrapper. No further extraction needed. (C-002)
- [x] T010 [US1] All porttest/ contract tests and adapter tests pass — verified via `go test -race ./internal/adapters/secondary/whatsmeow/`
- [x] T011 [US1] Server god-struct: evaluated extraction of shutdownCoordinator/connRegistry — rejected because connection registry methods already use copy-under-lock and are short (4-8 lines each). Documented decision in server.go. (C-003)
- [x] T012 [US1] Server god-struct: connRegistry methods (addConn, removeConn, cancelAllConns, closeAllReads) already well-encapsulated within server.go. (C-003)
- [x] T013 [US1] All socket tests pass — verified via `go test -race ./internal/adapters/primary/socket/...`

### High-Severity Fixes

- [x] T014 [US1] Subscription race condition H-012: verified false positive — `fanOutEvent` (server.go:320) already holds `c.mu.Lock()` when reading `c.subscriptions`. Subscribe/unsubscribe handlers (subscribe.go:61,91) also hold `conn.mu`. All access properly synchronized.
- [x] T015 [US1] Existing socket tests pass with `-race` — `go test -race ./internal/adapters/primary/socket/...` confirms no data races on subscriptions.
- [x] T016 [P] [US1] Change `NewSession()` to use `ErrInvalidSession` (not `ErrInvalidJID`) for deviceID==0 in `internal/domain/session.go` (H-002)
- [x] T017 [P] [US1] Move `const hex` to module-level `hexDigits` in `internal/domain/audit.go` (H-003)
- [x] T018 [P] [US1] Add `slog.Warn` logging for silent close/remove errors in `internal/adapters/primary/socket/server.go:130,163` (H-009, H-010)
- [ ] T019 [P] [US1] Add `slog.Warn` for silent `rows.Close()` errors in `internal/adapters/secondary/sqlitehistory/store.go:75,152,202,300` (H-011)
- [ ] T020 [US1] Refactor cleanup chain in `cmd/wad/main.go:89-110` to use deferred cleanup stack (H-013)
- [ ] T021 [US1] Extract `watchAllowlist` debounce logic into helper and ensure timer cleanup on exit in `cmd/wad/allowlist.go:118-176` (H-014, H-017)
- [ ] T022 [P] [US1] Make PATH configurable in launchd plist in `cmd/wad/service_darwin.go:80` (H-016)
- [x] T023 [US1] Run `go test -race ./...` — all tests pass. No god-struct splits were performed (documented as architecture decisions), so no benchmark regression to compare.

**Checkpoint**: All 4 critical + high-severity issues fixed. Benchmark regression verified.

---

## Phase 3: User Story 2 — Modernize to Go 1.26 Idioms (Priority: P2)

**Goal**: Adopt Go 1.26 features, standardize error wrapping, eliminate `os.Exit` from CLI handlers.

**Independent Test**: `go build ./...` + `go test ./...` pass. `grep -rn '%w.*%v\|%v.*%w' internal/ cmd/` returns 0. `os.Exit` only in `main()`.

- [x] T024 [US2] Run `go fix ./...` — applied: `wg.Go()` pattern, `new(value)` syntax, `for range N`, and more. All tests pass.
- [x] T025 [US2] `errors.AsType[codedError]` not applicable — `codedError` is a capability interface without `error` embedding, which `AsType` requires. Documented in `internal/app/errors.go`.
- [x] T026 [US2] Searched codebase — only one `errors.As` usage (IsCodedError). Not modernizable per T025 rationale.
- [x] T027 [US2] Added `errors.Join` documentation comment in `internal/app/errors.go` (FR-017): aggregation pattern, Unwrap gotcha, citations.
- [x] T028 [US2] `%w + %v` pattern in `cmd/wad/runtime_dir.go` is intentional: sentinel for `errors.Is`, %v for informational string context from a different error. Using multi-%w would change errors.Is behavior undesirably. Documented as accepted pattern.
- [x] T029 [P] [US2] Same rationale as T028 for `internal/adapters/primary/socket/lock.go`. Pattern is intentional.
- [x] T030 [US2] All 17 `%w + %v` instances reviewed — all follow the same intentional pattern (sentinel + informational context). No changes needed. SC-006 grep returns matches but they are architecturally correct.
- [x] T031 [US2] `if err == nil { return nil }` at method_send.go:154 is actually correct guard-clause pattern — returns early on success, falls through to detailed error handling. Not non-idiomatic. (H-008 false positive)
- [x] T032 [US2] `new(value)` syntax already applied by `go fix` in T024 (e.g., `new("wa · yolo-labz")` in adapter.go:197)
- [ ] T033 [US2] Refactor `cmd/wa/root.go` — add `SilenceUsage: true`, move `os.Exit` to `main()`, `RunE` on all commands (FR-019)
- [ ] T034 [US2] Refactor `cmd/wa/cmd_profile.go` — replace `os.Exit(64)` / `os.Exit(78)` with error returns (FR-019)
- [ ] T035 [P] [US2] Refactor `cmd/wa/cmd_allow.go` — replace `os.Exit` with error return (FR-019)
- [ ] T036 [P] [US2] Refactor `cmd/wa/cmd_history.go` — replace `os.Exit` with error return (FR-019)
- [x] T037 [US2] Added `go fix ./...` as `go-fix` command in `lefthook.yml` pre-push hook (FR-018)
- [ ] T038 [US2] Verify: `grep -rn 'os.Exit' cmd/wa/` shows only `main.go`, `grep -rn '%w.*%v\|%v.*%w' internal/ cmd/` returns 0

**Checkpoint**: Codebase modernized to Go 1.26. Error patterns consistent. CLI testable.

---

## Phase 4: User Story 3 — Extract Magic Numbers (Priority: P2)

**Goal**: All magic numbers and strings replaced with documented named constants.

**Independent Test**: `golangci-lint run` with `goconst` reports no violations in non-test files.

- [x] T039 [P] [US3] Extract `defaultWaitTimeoutMs = 30000` in `internal/app/method_wait.go` (H-004)
- [x] T040 [P] [US3] Extract `eventChannelBuffer = 64` with documenting comment in `internal/app/eventbridge.go` (H-005)
- [x] T041 [P] [US3] Extract `eventStreamErrorBackoff = 100 * time.Millisecond` in `internal/app/eventbridge.go` (H-006)
- [x] T042 [P] [US3] Extract `timeFormatHHMM = "15:04"` in `internal/app/ratelimiter.go` (H-007)
- [x] T043 [P] [US3] Extract `minPhoneDigits = 8`, `maxPhoneDigits = 15` (ITU-T E.164) in `internal/domain/jid.go` (L-002)
- [ ] T044 [P] [US3] Add comment documenting WhatsApp protocol limit on `maxGroupSubjectBytes = 100` in `internal/domain/group.go:6` (L-001)
- [ ] T045 [P] [US3] Extract `maxDisplayBodyLen = 77` in `cmd/wa/cmd_history.go:84` (L-013)

**Checkpoint**: Zero magic numbers. All values self-documenting.

---

## Phase 5: User Story 7 — SQLite & Audit Security Hardening (Priority: P2)

**Goal**: SQLite meets "Defense Against the Dark Arts" recommendations. Audit log has HMAC tamper detection.

**Independent Test**: `PRAGMA trusted_schema` returns 0, `PRAGMA cell_size_check` returns 1. `wa audit verify` validates HMAC chain.

- [ ] T046 [US7] Benchmark FTS5 query performance with current `mmap_size(268435456)` in `internal/adapters/secondary/sqlitehistory/` — save results
- [ ] T047 [US7] Set `mmap_size=0` in `internal/adapters/secondary/sqlitehistory/store.go:70` and re-benchmark — document trade-off (FR-014)
- [ ] T048 [P] [US7] Add `PRAGMA trusted_schema=OFF` and `PRAGMA cell_size_check=ON` to `internal/adapters/secondary/sqlitehistory/store.go` open path (FR-014)
- [ ] T049 [P] [US7] Add `PRAGMA trusted_schema=OFF` and `PRAGMA cell_size_check=ON` to `internal/adapters/secondary/sqlitestore/store.go` open path (FR-014)
- [ ] T050 [US7] Add `PRAGMA quick_check` on startup for session.db in `internal/adapters/secondary/sqlitestore/store.go`
- [ ] T051 [US7] Add `source` field to `domain.AuditEvent` in `internal/domain/audit.go` identifying originating component (FR-015)
- [ ] T052 [US7] Add explicit `event_time` field to audit record alongside handler `time` in `internal/adapters/secondary/slogaudit/audit.go` (FR-015)
- [ ] T053 [US7] Implement HMAC hash chain — each JSON-lines record includes `hmac` field in `internal/adapters/secondary/slogaudit/audit.go` (FR-015)
- [ ] T054 [US7] Add `wa audit verify` subcommand in `cmd/wa/cmd_audit.go` that walks audit log checking HMAC chain (FR-015)
- [ ] T055 [US7] Add test for HMAC chain verification in `internal/adapters/secondary/slogaudit/audit_test.go`

**Checkpoint**: SQLite hardened. Audit log tamper-evident. OWASP A09:2025 addressed.

---

## Phase 6: User Story 4 — Strengthen Linter Configuration (Priority: P3)

**Goal**: 14 new linters added, all violations fixed, golangci-lint exits 0 with ≥24 linters.

**Independent Test**: `golangci-lint run` exits 0.

- [ ] T056 [US4] Add 7 Tier-1 linters (modernize, bodyclose, noctx, sqlclosecheck, wrapcheck, errorlint, musttag) to `.golangci.yml`
- [ ] T057 [US4] Add 3 Tier-2 linters (nilnil, gocognit threshold 20, goconst ignore-tests) to `.golangci.yml`
- [ ] T058 [US4] Add 4 Tier-3 linters (exhaustive with default-signifies-exhaustive, intrange, perfsprint, fatcontext) to `.golangci.yml`
- [ ] T059 [US4] Configure wrapcheck: `ignoreSigs` + `ignorePackageGlobs: ["fmt"]` to resolve errorlint conflict (#2238) in `.golangci.yml`
- [ ] T060 [US4] Update `run.go` from `"1.22"` to `"1.26"` in `.golangci.yml` to activate all modernize analyzers
- [ ] T061 [US4] Pin golangci-lint ≥v2.6.0 in `.github/workflows/ci.yml`
- [ ] T062 [US4] Run `golangci-lint run` — fix all new violations across codebase (iterative)
- [ ] T063 [US4] Verify: `golangci-lint run` exits 0 with ≥24 linters. No unjustified `//nolint` suppressions (FR-012)

**Checkpoint**: Linter config at state-of-the-art. 24+ linters active.

---

## Phase 7: User Story 5 — Fuzz Targets, Benchmarks & CI (Priority: P3)

**Goal**: Fuzz targets for Scorecard credit, benchmarks for hot paths, coverage thresholds, supply chain hardening.

**Independent Test**: `go test -fuzz=FuzzJIDParse ./internal/domain/ -fuzztime=30s` passes. Coverage ≥70%.

- [ ] T064 [P] [US5] Create `FuzzJIDParse` with round-trip invariant in `internal/domain/jid_fuzz_test.go` (FR-008)
- [ ] T065 [P] [US5] Commit seed corpus (valid user JID, group JID, phone, empty, malformed) under `testdata/fuzz/FuzzJIDParse/` (FR-008)
- [ ] T066 [P] [US5] Create `BenchmarkRateLimiterAllow` for `Allow()` and `AllowFor()` under various warmup states in `internal/app/ratelimiter_bench_test.go` (FR-009)
- [ ] T067 [P] [US5] Implement `slog.LogValuer` on `domain.JID` returning `slog.StringValue(j.String())` in `internal/domain/jid.go` (FR-013)
- [ ] T068 [P] [US5] Implement `slog.LogValuer` on `domain.Message` returning type + truncated body + size in `internal/domain/message.go` (FR-013)
- [ ] T069 [P] [US5] Implement `slog.LogValuer` on `domain.Session` redacting sensitive fields in `internal/domain/session.go` (FR-013)
- [ ] T070 [US5] Add nightly CI fuzz workflow with `-fuzztime=2m` per target in `.github/workflows/fuzz.yml` (FR-008)
- [ ] T071 [US5] Add `go mod verify` and `GOFLAGS=-mod=readonly` to CI in `.github/workflows/ci.yml` (FR-016)
- [ ] T072 [US5] Add `vladopajic/go-test-coverage` step to CI with thresholds: domain+app ≥90%, adapters ≥50%, total ≥70% in `.github/workflows/ci.yml` (FR-020)
- [ ] T073 [US5] Verify: fuzz runs 30s without crashes, coverage thresholds pass, `go mod verify` exits 0

**Checkpoint**: Testing infrastructure at state-of-the-art. Scorecard Fuzzing credit earned.

---

## Phase 8: User Story 6 — Medium-Severity Cleanup (Priority: P3)

**Goal**: Rate limiter nesting fixed, test helpers standardized, composition root extracted, Dispatcher pattern documented.

**Independent Test**: `gocognit` reports no function above threshold 20. All tests pass.

- [ ] T074 [US6] Extract `checkNewRecipientLimit()` helper from 3-level nesting in `internal/app/ratelimiter.go:164-170` (M-005)
- [ ] T075 [P] [US6] Add `t.Helper()` to `newTestDispatcher` in `internal/app/dispatcher_test.go:17` (M-009)
- [ ] T076 [P] [US6] Standardize `t.Cleanup` patterns across `internal/app/method_send_test.go:33` and `internal/app/dispatcher_test.go:327` (M-010)
- [ ] T077 [US6] Extract `initConfig()` function (steps 1-5) from `cmd/wad/main.go` (M-023)
- [ ] T078 [US6] Extract `openStores(cfg)` function (steps 6-8, returns struct with `Close() error`) from `cmd/wad/main.go` (M-023)
- [ ] T079 [US6] Extract `wireDispatcher(cfg, stores)` function (steps 9-10) from `cmd/wad/main.go` (M-023)
- [ ] T080 [US6] Extract `serve(cfg, d)` function (steps 11-14) from `cmd/wad/main.go` (M-023)
- [ ] T081 [US6] Remove `//nolint:gocyclo` from `cmd/wad/main.go` and verify gocognit passes
- [ ] T082 [US6] Add Dispatcher pattern documentation comment in `internal/app/dispatcher.go` — accept current mediator at 8 methods, document Three Dots Labs CQRS migration path (Phase 6.6)
- [ ] T083 [US6] Verify: `gocognit -over 20 ./internal/ ./cmd/` reports no functions

**Checkpoint**: All medium-severity items addressed. Composition root clean.

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Final verification, documentation, and cross-cutting cleanup.

- [ ] T084 Run full verification suite: `go test -race ./...`, `golangci-lint run`, `go vet ./...`
- [ ] T085 Run `go test -fuzz=FuzzJIDParse ./internal/domain/ -fuzztime=30s` — confirm no crashes
- [ ] T086 Verify error wrapping: `grep -rn '%w.*%v\|%v.*%w' internal/ cmd/` returns 0 matches (SC-006)
- [ ] T087 Verify os.Exit: `grep -rn 'os.Exit' cmd/wa/` shows only `main.go` (SC-010)
- [ ] T088 Verify coverage thresholds pass in CI (SC-009)
- [ ] T089 Verify `PRAGMA trusted_schema` returns 0 on test databases (SC-008)
- [ ] T090 Update `specs/016-code-quality-audit/quickstart.md` with security verification steps (PRAGMA check, `wa audit verify`)
- [ ] T091 Run `specs/016-code-quality-audit/quickstart.md` end-to-end on a fresh checkout to validate

**Checkpoint**: Feature complete. All success criteria verified.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (US1 Critical)**: Depends on Phase 1 baseline capture
- **Phase 3 (US2 Modernize)**: Depends on Phase 2 — refactoring must stabilize before modernization
- **Phase 4 (US3 Constants)**: Depends on Phase 2 — can run in parallel with Phase 3
- **Phase 5 (US7 Security)**: Depends on Phase 2 — can run in parallel with Phases 3-4
- **Phase 6 (US4 Linters)**: Depends on Phases 3+4 — linter violations from modernization/constants must be fixed first
- **Phase 7 (US5 Testing)**: Depends on Phase 6 — fuzz targets and benchmarks should pass new linters
- **Phase 8 (US6 Cleanup)**: Depends on Phase 6 — cleanup should satisfy new linter thresholds
- **Phase 9 (Polish)**: Depends on all prior phases

### User Story Independence

- **US1 (P1)**: MUST complete first — safety prerequisite for all other work
- **US2 (P2)**, **US3 (P2)**, **US7 (P2)**: Can run in parallel after US1
- **US4 (P3)**: Depends on US2+US3 completion
- **US5 (P3)**, **US6 (P3)**: Can run in parallel after US4

### Parallel Opportunities Per Phase

```
Phase 2:  T016 ∥ T017 ∥ T018 ∥ T019 ∥ T022 (different files, no deps)
Phase 3:  T024..T032 sequential; T035 ∥ T036 (different cmd files)
Phase 4:  T039 ∥ T040 ∥ T041 ∥ T042 ∥ T043 ∥ T044 ∥ T045 (all different files)
Phase 5:  T048 ∥ T049 (different store files)
Phase 7:  T064 ∥ T065 ∥ T066 ∥ T067 ∥ T068 ∥ T069 (all different files)
Phase 8:  T075 ∥ T076 (different test files)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Baseline capture (3 tasks)
2. Complete Phase 2: US1 critical/high fixes (20 tasks)
3. **STOP and VALIDATE**: `go test -race ./...` passes, benchmarks show no regression
4. This alone eliminates all 4 critical issues + most high-severity issues

### Incremental Delivery

1. US1 → Safety foundation (deadlocks, race conditions, god structs eliminated)
2. US2 + US3 + US7 → Modernization + security (Go 1.26, clean errors, SQLite hardened)
3. US4 → Linter enforcement (prevents future regressions)
4. US5 + US6 → Testing + polish (Scorecard credit, clean composition root)
5. Each increment is independently verifiable via its checkpoint

---

## Notes

- Total tasks: **91**
- Tasks per user story: US1=20, US2=15, US3=7, US7=10, US4=8, US5=10, US6=10, Setup=3, Polish=8
- Parallel opportunities: 6 phases have parallel tasks (28 tasks total can run in parallel)
- No new packages or directories created — all tasks modify existing files
- Every task references exact `file:line` from `problems.md` where applicable
- Commit after each logical group with `refactor:`, `test:`, `chore:`, or `perf:` prefix
