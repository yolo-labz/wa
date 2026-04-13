# Implementation Plan: Code Quality Audit & Modernization

**Branch**: `016-code-quality-audit` | **Date**: 2026-04-13 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/016-code-quality-audit/spec.md`

## Summary

Systematic code quality improvement driven by a 9-agent audit swarm that identified 75 issues (4 critical, 17 high, 28 medium, 26 low) and researched bleeding-edge Go 2025-2026 practices. The implementation fixes all critical and high-severity issues, modernizes to Go 1.26 idioms, strengthens the linter configuration with 10 additional linters, and adds fuzz targets and benchmarks. No new features, APIs, or data models — purely structural and quality improvements that preserve all existing behavior.

## Technical Context

**Language/Version**: Go 1.26.1 (toolchain pinned in `go.mod`)
**Primary Dependencies**: whatsmeow (commit-pinned), modernc.org/sqlite, spf13/cobra, golang.org/x/time/rate, creachadair/jrpc2
**Storage**: SQLite (session.db, messages.db) — unchanged by this feature
**Testing**: `go test -race ./...`, `testing/synctest` (Go 1.25 GA), fuzz testing, `golangci-lint run`
**Target Platform**: darwin/arm64, linux/amd64, linux/arm64 — unchanged
**Project Type**: CLI + daemon (hexagonal architecture)
**Performance Goals**: No regression — benchmarks establish baseline, not targets
**Constraints**: CGO_ENABLED=0 (invariant), all existing tests must pass, no public API changes
**Scale/Scope**: ~8,800 lines across 54 non-test Go files + 56 test files

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|---|---|---|
| **I. Hexagonal core, library at arm's length** | PASS | Refactoring preserves the boundary. God-struct splits keep `depguard` enforcement intact. No new whatsmeow imports in core. |
| **II. Daemon owns state, CLI is dumb** | PASS | No logic moves between `wad` and `wa`. CLI handler refactoring (os.Exit → error returns) is internal to `cmd/wa`. |
| **III. Safety first, no --force ever** | PASS | Rate limiter, allowlist, warmup, and audit log are untouched functionally. Refactoring improves their code quality without changing behavior. |
| **IV. CGO is forbidden** | PASS | No new dependencies. All linter additions are build-time-only tools. |
| **V. Spec-driven development with citations** | PASS | `problems.md` catalogs all issues with `file:line` references. `research.md` cites 30+ primary sources (Go blog, GopherCon, Uber/Google guides, package docs). |
| **VI. Tests use port-boundary fakes** | PASS | New tests (fuzz targets, benchmarks, deadlock regression) use the existing `memory/` adapter and `porttest/` contract suite. No new whatsmeow imports outside integration build tag. |
| **VII. Conventional commits, signed tags** | PASS | All commits will follow `refactor:`, `test:`, `chore:` prefixes. No `--no-verify`. |

**Gate result**: ALL PASS — proceed to implementation planning.

## Project Structure

### Documentation (this feature)

```text
specs/016-code-quality-audit/
├── plan.md              # This file
├── spec.md              # Feature specification (6 user stories, 13 FRs)
├── problems.md          # Phase 0 output: 75 issues cataloged by severity
├── research.md          # Phase 0 output: Go 2025-2026 best practices
├── checklists/
│   └── requirements.md  # Spec quality validation (18/18 passing)
└── tasks.md             # Phase 2 output (created by /speckit:tasks)
```

### Source Code (repository root)

This feature modifies existing files only — no new directories or packages. Files touched, organized by implementation phase:

```text
# Phase 1: Critical & High fixes
internal/app/eventbridge.go          # C-001: deadlock fix (copy waiters under lock, iterate without)
internal/adapters/secondary/whatsmeow/adapter.go  # C-002: split into sub-structs
internal/adapters/primary/socket/server.go         # C-003: extract shutdown orchestrator
internal/domain/allowlist.go          # C-004: Grant() zero-value fix
internal/adapters/primary/socket/connection.go     # H-012: subscription access under mutex
internal/domain/session.go            # H-002: correct error sentinel
internal/domain/audit.go              # H-003: module-level hex constant
cmd/wad/main.go                       # H-013: cleanup chain DRY refactor
cmd/wad/allowlist.go                  # H-014, H-017: watchAllowlist complexity + timer cleanup
cmd/wad/service_darwin.go             # H-016: configurable PATH

# Phase 2: Go 1.26 modernization
internal/app/errors.go                # FR-005: errors.AsType
internal/app/method_send.go           # H-008: line-of-sight fix
cmd/wad/runtime_dir.go                # M-026: error wrapping standardization
internal/adapters/primary/socket/lock.go  # M-026: error wrapping
# + all files touched by `go fix ./...`

# Phase 3: Named constants
internal/app/method_wait.go           # H-004: defaultWaitTimeoutMs
internal/app/eventbridge.go           # H-005: eventChannelBuffer, H-006: errorBackoff
internal/app/ratelimiter.go           # H-007: timeFormatHHMM
internal/domain/jid.go                # L-002: MinPhoneDigits, MaxPhoneDigits
internal/domain/group.go              # L-001: document maxGroupSubjectBytes source

# Phase 4: Linter config
.golangci.yml                         # FR-007: add 10 linters + settings

# Phase 5: Testing additions
internal/domain/jid_fuzz_test.go      # FR-008: FuzzJIDParse
testdata/fuzz/FuzzJIDParse/           # FR-008: seed corpus
internal/app/ratelimiter_bench_test.go # FR-009: BenchmarkRateLimiterAllow
internal/app/eventbridge_test.go      # C-001 regression test
internal/domain/jid.go                # FR-013: slog.LogValuer on JID

# Phase 6: Medium-severity cleanup (opportunistic)
internal/app/ratelimiter.go           # M-005: extract checkNewRecipientLimit
internal/app/dispatcher_test.go       # M-009: t.Helper()
cmd/wa/cmd_profile.go                 # M-021: os.Exit → error returns (if feasible)
```

**Structure Decision**: No new packages or directories. This feature is a pure refactoring that improves the internal quality of existing code while preserving all external behavior and the hexagonal architecture.

## Implementation Phases

### Phase 1: Critical & High Safety Fixes (P1)

**Goal**: Eliminate all 4 critical issues and address the highest-impact high-severity issues.

**1.1 EventBridge deadlock fix (C-001)**
- Apply the **copy-under-lock** pattern (production consensus per Watermill `gochannel/pubsub.go`, NATS Go client, Eli Bendersky's PubSub pattern): acquire `b.mu`, copy `b.waiters` into a local slice, release `b.mu`, then iterate the copy and send to channels
- Consider `go-deadlock` (`github.com/sasha-s/go-deadlock`) as a drop-in `sync.Mutex` replacement during development to validate the fix detects no ordering violations
- Add regression test using `synctest` with concurrent cancel + event delivery
- File: `internal/app/eventbridge.go:93-105`

**1.2 Whatsmeow Adapter god-struct split (C-002)**
- Sub-structs remain **in the same package** (rationale: Three Dots Labs wild-workouts keeps all adapter code in one package; Sam Smith's hexagonal Go guide says "keep all code relating to a single external service in the same package"; extracting sub-packages would force shared fields like `client`, `clientCtx`, `logger` to become exported)
- Extract `historySyncWorker` (historyReqs, historySyncCh, historySyncWg, isSyncing — already has distinct state in `history_sync.go`)
- Extract `auditWriter` (auditBuf, audit-related methods)
- Keep `Adapter` as facade composing sub-structs via **named fields** (not embedding, to avoid method set pollution — Prometheus pattern)
- Target: reduce cognitive load per responsibility, not a hard field-count limit (no Go community consensus exists on a magic number — the "≤7 fields" from the original plan is replaced by the SRP heuristic from "Exploding Large Go Structs" by Breaking Computer)
- **Testing strategy**: existing `porttest/` contract tests are the primary safety net (Watermill pattern). Add unit tests for extracted sub-struct methods. Do NOT move existing test files.
- **Run benchmarks before and after** to catch performance regressions from added indirection
- File: `internal/adapters/secondary/whatsmeow/adapter.go`

**1.3 Socket Server god-struct split (C-003)**
- Extract `shutdownCoordinator` (shutdown flag, deadline, drain logic from lines 130-175)
- Extract `connRegistry` (connections map, mutex, add/remove methods)
- Keep `Server` as composition of coordinator + registry + listener via named fields
- Same-package rationale and testing strategy as 1.2
- File: `internal/adapters/primary/socket/server.go`

**1.4 Allowlist Grant() edge case (C-004)**
- Add explicit existence check before modifying `set`
- Ensure `Grant()` on a previously empty entry persists correctly
- Add test for the specific edge case
- File: `internal/domain/allowlist.go:92-105`

**1.5 Subscription race condition (H-012)**
- Ensure `fanOutEvent` accesses `Connection.subscriptions` under the connection's mutex
- Verify with `-race` detector in new test
- File: `internal/adapters/primary/socket/connection.go`

**1.6 Other high-severity fixes**
- H-002: Change `NewSession()` to use `ErrInvalidDeviceID` instead of `ErrInvalidJID` for deviceID==0
- H-003: Move `const hex` to module-level in `domain/audit.go`
- H-009/H-010/H-011: Add `slog.Warn` logging for silent close/remove errors in shutdown paths
- H-013: Refactor `cmd/wad/main.go` cleanup chain to use a deferred cleanup stack
- H-014/H-017: Extract `watchAllowlist` debounce logic into helper; ensure timer cleanup on exit

### Phase 2: Go 1.26 Modernization (P2)

**Goal**: Adopt Go 1.26 features and standardize error patterns.

**2.1 Run `go fix ./...`**
- **Prerequisite**: verify `go.mod` declares `go 1.26` (already the case) — `go fix` respects this directive and won't apply modernizations beyond it
- Apply auto-modernization across all files
- Review diff, revert any breaking changes
- Run `go build ./...` and `go test ./...` to verify
- **Add `go fix ./...` as a recurring `lefthook` pre-push hook** (not just one-time) — the Go team recommends running it on every toolchain upgrade to catch LLM-generated old-style patterns

**2.2 `errors.AsType` migration**
- Replace `errors.As` with `errors.AsType` in `internal/app/errors.go:46-52`
- Search for other `errors.As` usages and modernize where straightforward
- Target `new(value)` syntax adoption in JSON-RPC result structs where optional pointer fields use `ptr[T]` helpers (e.g., pair results with `Code *string`)

**2.3 Error wrapping standardization**
- Find all `%w.*%v` and `%v.*%w` patterns
- Standardize to `fmt.Errorf("context: %w", err)` with single `%w` verb
- **Document `errors.Join` as the distinct pattern** for aggregating independent errors (cleanup, validation, parallel fan-out). Add a code comment in `internal/app/errors.go` noting the `errors.Unwrap()` nil-return gotcha with `errors.Join` (Ian Lewis TIL, March 2025, golang/go#57358)
- Files: `cmd/wad/runtime_dir.go`, `internal/adapters/primary/socket/lock.go`, 10+ others

**2.4 Line-of-sight fix**
- Refactor `method_send.go:154` from `if err == nil` to early-return `if err != nil`

**2.5 os.Exit refactoring (elevated from Phase 6 to P2)**
- Community consensus confirms this is a testability prerequisite (gh CLI uses `exitCode` type with `os.Exit` only in `main()`, GoReleaser injects `os.Exit` as a function parameter, Cobra issues #914/#2124)
- Refactor CLI handlers to return errors; add `SilenceUsage: true` on root command
- Pattern: `func main() { os.Exit(run()) }` with `run()` calling `rootCmd.Execute()` and mapping errors to sysexits.h codes
- Files: `cmd/wa/root.go`, `cmd/wa/cmd_profile.go`, `cmd/wa/cmd_allow.go`, `cmd/wa/cmd_history.go`

### Phase 3: Named Constants (P2)

**Goal**: Replace all magic numbers/strings with documented named constants.

| Magic Value | Constant Name | File |
|---|---|---|
| `30000` | `defaultWaitTimeoutMs` | `internal/app/method_wait.go:31` |
| `64` (chan buffer) | `eventChannelBuffer` | `internal/app/eventbridge.go:52` |
| `100 * time.Millisecond` | `eventStreamErrorBackoff` | `internal/app/eventbridge.go:75` |
| `"15:04"` | `timeFormatHHMM` | `internal/app/ratelimiter.go:160` |
| `8`, `15` (phone digits) | `minPhoneDigits`, `maxPhoneDigits` | `internal/domain/jid.go:72-73` |
| `100` (group subject) | Document as WhatsApp protocol limit | `internal/domain/group.go:6` |
| `77` (truncation) | `maxDisplayBodyLen` | `cmd/wa/cmd_history.go:84` |

### Phase 4: Linter Configuration (P3)

**Goal**: Add 14 linters to `.golangci.yml` and achieve clean `golangci-lint run`.

**4.1 Add Tier-1 linters** (7):
`modernize`, `bodyclose`, `noctx`, `sqlclosecheck`, `wrapcheck`, `errorlint`, `musttag`

**4.2 Add Tier-2 linters** (3):
`nilnil`, `gocognit` (threshold 20), `goconst` (ignore tests, min 3 occurrences)

**4.3 Add Tier-3 linters** (4 — identified by checklist research, validated against maratori golden config):
- `exhaustive` with `default-signifies-exhaustive: true` (catches missing switch cases on `domain.Action`, `domain.MessageType`; 3 of 4 golden configs enable it)
- `intrange` (detects `for i := 0; i < len(x); i++` replaceable with `for i := range len(x)` — zero false positives, Go 1.22+)
- `perfsprint` (catches `fmt.Sprintf` replaceable with `strconv` — measurably faster in hot paths)
- `fatcontext` (detects `context.WithValue`/`context.WithCancel` inside loops — relevant for daemon code)

**4.4 Configure settings**:
- `wrapcheck.ignoreSigs`: `io.EOF`, `context.Canceled`, `context.DeadlineExceeded`
- `wrapcheck.ignorePackageGlobs`: `["fmt"]` — **resolves the documented wrapcheck/errorlint conflict** (golangci-lint issue #2238) where both fire on the same `fmt.Errorf` with contradictory advice
- `goconst.ignore-tests: true` (2 of 4 golden configs skip goconst entirely due to test noise)
- `gocognit.min-complexity: 20`
- `exhaustive.default-signifies-exhaustive: true`
- **Update `run.go` from `"1.22"` to `"1.26"`** to match the actual toolchain in `go.mod` — this affects which `modernize` analyzers activate
- **Pin golangci-lint ≥v2.6.0** in CI (required for `modernize` linter)

**4.5 Fix violations**:
- Run `golangci-lint run` with new config
- Fix all violations (or add justified `//nolint` with comments per FR-012)
- Iterate until `golangci-lint run` exits 0

### Phase 5: Testing & Observability Additions (P3)

**Goal**: Add fuzz targets, benchmarks, `slog.LogValuer`, coverage thresholds, and CI fuzz workflow.

**5.1 Fuzz target: `FuzzJIDParse`**
- File: `internal/domain/jid_fuzz_test.go`
- Seed corpus: valid user JID, group JID, phone number, empty string, malformed strings
- Corpus committed under `testdata/fuzz/FuzzJIDParse/`
- Tests round-trip invariant: `Parse(j.String()) == j`
- **Add nightly CI workflow** running fuzz targets with `-fuzztime=2m` per target (community recommendation: PR checks 30s-2m, nightly 5-10m)

**5.2 Benchmark: `BenchmarkRateLimiterAllow`**
- File: `internal/app/ratelimiter_bench_test.go`
- Benchmarks `Allow()` and `AllowFor()` under various warmup states
- Establishes baseline ns/op for regression detection
- **Run before and after Phase 1 god-struct refactoring** to catch performance regressions from added indirection

**5.3 `slog.LogValuer` on domain types**
- `domain.JID`: `LogValue()` returns `slog.StringValue(j.String())`
- `domain.Message`: `LogValue()` returns type + truncated body (≤80 chars) + size — redacts full content
- `domain.Session`: `LogValue()` redacts sensitive device identity fields
- Files: `internal/domain/jid.go`, `internal/domain/message.go`, `internal/domain/session.go`

**5.4 EventBridge deadlock regression test**
- Uses `synctest.Test` with concurrent waiter cancel + event delivery
- Specific synctest expansion targets: EventBridge timeout behavior, subscribe/wait timeout logic, reconnect state transitions
- Verifies no goroutine leaks via `goleak`

**5.5 Coverage thresholds**
- Add `vladopajic/go-test-coverage` (or equivalent) as a CI step reading existing `cover.out`
- Thresholds: domain+app ≥90%, adapters ≥50%, cmd ≥40%, total ≥70%
- Rationale: adapter layer legitimately has code paths only reachable with real WhatsApp; domain/app are pure logic with in-memory fakes

**5.6 Supply chain hardening**
- Add `go mod verify` to CI (one-line addition, confirms modules match `go.sum`)
- Set `GOFLAGS=-mod=readonly` in CI to prevent silent `go.mod`/`go.sum` updates during build

### Phase 6: Medium-Severity Cleanup (P3)

**Goal**: Address remaining medium issues. os.Exit refactoring moved to Phase 2.5.

**6.1 Rate limiter nesting** (M-005)
- Extract 3-level nesting in `checkRecipientLimits` to `checkNewRecipientLimit()` helper
- File: `internal/app/ratelimiter.go:164-170`

**6.2 Test helpers** (M-009, M-010)
- Add `t.Helper()` to `newTestDispatcher` and similar helpers
- Standardize `t.Cleanup` patterns

**6.3 Composition root sub-functions** (M-023, M-024)
- Extract into named phase functions following the gh CLI / GoReleaser pattern:
  - `initConfig()` → steps 1-5: logging, profile, migration, dirs
  - `openStores(cfg)` → steps 6-8: session, history, audit, allowlist (returns struct with `Close() error`)
  - `wireDispatcher(cfg, stores)` → steps 9-10: adapter, dispatcher
  - `serve(cfg, d)` → steps 11-14: socket server, signal, shutdown
- Each phase function returns a struct with a `Close() error` method, eliminating manual teardown chains
- **Rejected alternatives**: Wire (adds code gen step, overkill for ~10 deps — Redowan), Fx (runtime DI with ~3.8ms overhead, massive overkill — Leapcell comparison). Manual DI is correct for this project's scale.
- Reduces `run()` cognitive complexity below gocognit threshold; removes `//nolint:gocyclo`

**6.4 SQLite security PRAGMAs** (FR-014)
- Add `PRAGMA trusted_schema=OFF` and `PRAGMA cell_size_check=ON` to both `sqlitestore/store.go` and `sqlitehistory/store.go` open paths
- Set `mmap_size=0` in `sqlitehistory/store.go` (currently 268435456) per SQLite's "Defense Against the Dark Arts" security guidance — mmap exposes process to SIGBUS on corruption and removes SQLite's ability to detect certain corruptions
- Benchmark FTS5 query performance before/after mmap removal; document trade-off
- Add `PRAGMA quick_check` on startup for session.db to detect corruption from unclean shutdown

**6.5 Audit log security hardening** (FR-015)
- Add HMAC hash chain: each JSON-lines record includes `hmac` field = `HMAC-SHA256(record, key)` with key derived from daemon session identity
- Add `source` field identifying originating component (e.g., `socket-rpc`, `allowlist-reload`)
- Add explicit `event_time` field alongside handler-generated `time` (OWASP Logging Cheat Sheet)
- Add `wa audit verify` subcommand that walks the log checking each HMAC

**6.6 Dispatcher pattern documentation**
- The Dispatcher's 15-field/16-method structure is **explicitly accepted at current scale** (8 methods). The Three Dots Labs per-handler CQRS pattern (each handler struct receives only ports it uses) is the recommended migration path if/when method count exceeds ~15
- Document this decision in a code comment on `dispatcher.go`

## Dependency Graph

```text
Phase 1 (critical/high fixes + benchmarks before/after)
  └─→ Phase 2 (Go 1.26 modernization + os.Exit refactor)  ─→  Phase 4 (linter config)
  └─→ Phase 3 (named constants)                            ─→  Phase 4 (linter config)
                                                                 └─→ Phase 5 (testing + CI)
                                                                 └─→ Phase 6 (medium cleanup + security)
```

- Phases 2 and 3 can run in parallel after Phase 1
- Phase 4 depends on Phases 2+3 (linter violations from modernization must be fixed first)
- Phases 5 and 6 can run in parallel after Phase 4
- Phase 6.4 (SQLite PRAGMAs) and 6.5 (audit HMAC) are independent of other Phase 6 items

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| God-struct split breaks adapter API | Medium | High | Keep facade type, delegate to sub-structs. Run full test suite after each split. |
| `go fix` introduces compilation errors | Low | Low | Run `go build ./...` immediately after. Revert individual auto-fixes if broken. |
| New linters produce excessive violations | Medium | Medium | Fix in batches. Use `//nolint` with justification for false positives. |
| Error wrapping changes break `errors.Is` chains | Low | High | Test all sentinel error matches after wrapping changes. |
| Refactoring introduces subtle behavior changes | Low | High | No functional changes — only structural. Full `-race` test suite is the safety net. |

## Verification Strategy

Each phase has a clear verification checkpoint:

1. **Phase 1**: `go test -race ./...` passes. New regression tests for C-001, C-004, H-012 are green. Benchmark before/after shows no >10% regression.
2. **Phase 2**: `go build ./...` and `go test ./...` pass. `grep -rn '%w.*%v\|%v.*%w' internal/ cmd/` returns 0. `os.Exit` calls only in `main()` (verified by `grep -rn 'os.Exit' cmd/`).
3. **Phase 3**: No magic numbers in `golangci-lint` `goconst` output (test files excluded).
4. **Phase 4**: `golangci-lint run` exits 0 with ≥24 linters enabled. `run.go` version is `"1.26"`.
5. **Phase 5**: `go test -fuzz=FuzzJIDParse ./internal/domain/ -fuzztime=30s` passes. Coverage thresholds: domain+app ≥90%, total ≥70%. `go mod verify` exits 0.
6. **Phase 6**: `gocognit` reports no function above threshold 20. `PRAGMA trusted_schema` returns 0. `wa audit verify` passes on test log.

## Complexity Tracking

No constitution violations — table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| *(none)* | | |
