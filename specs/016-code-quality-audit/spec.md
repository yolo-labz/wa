# Feature Specification: Code Quality Audit & Modernization

**Feature Branch**: `016-code-quality-audit`
**Created**: 2026-04-13
**Status**: Draft
**Input**: User description: "Deploy agent swarms to identify code smells and compile into problems.md, then research bleeding-edge Go practices for code quality and readability excellence, compiled into research.md"

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Fix Critical Safety Issues (Priority: P1)

As a developer, I want all critical and high-severity code smells fixed so that the codebase is free of deadlock risks, race conditions, and god-struct maintenance burden.

**Why this priority**: Critical issues (C-001 EventBridge deadlock, C-002/C-003 god structs, H-012 race condition) are safety and correctness risks that affect production reliability.

**Independent Test**: Can be verified by running `go test -race ./...` with new tests targeting the fixed code paths, plus `golangci-lint run` passing without new `//nolint` suppressions.

**Acceptance Scenarios**:

1. **Given** the EventBridge `Run()` method holds `b.mu` while sending to waiter channels, **When** a waiter's cancel function fires concurrently, **Then** no deadlock occurs (verified by a test using `synctest` with concurrent cancel + event delivery).
2. **Given** the whatsmeow `Adapter` struct has 13 fields, **When** it is refactored into focused sub-structs, **Then** no single struct exceeds 7 fields AND all existing tests pass.
3. **Given** the socket `Server` struct has 13+ fields, **When** shutdown orchestration is extracted, **Then** the connection registry, shutdown coordinator, and server core are separate types AND all existing tests pass.
4. **Given** `Connection.subscriptions` is accessed from multiple goroutines, **When** `fanOutEvent` reads subscriptions, **Then** it does so under the connection's mutex (verified by `-race` detector).

---

### User Story 2 — Modernize to Go 1.26 Idioms (Priority: P2)

As a developer, I want the codebase to adopt Go 1.26 features (`errors.AsType`, `new(value)`, `go fix` modernization) and standardize error wrapping patterns so the code signals modernity and follows current best practices.

**Why this priority**: These are low-risk, high-readability improvements. `errors.AsType` is 3x faster than `errors.As`. `go fix` auto-modernizes patterns across all files. Standardizing error wrapping (`%w` only, no `%w + %v` mixing) improves consistency.

**Independent Test**: Can be verified by running `go fix ./...`, confirming `golangci-lint run` passes, and spot-checking that `errors.AsType` replaces `errors.As` in `internal/app/errors.go`.

**Acceptance Scenarios**:

1. **Given** `IsCodedError` uses `errors.As`, **When** modernized, **Then** it uses `errors.AsType[codedError]` and all error-matching tests pass.
2. **Given** 10+ files use `fmt.Errorf("%w: ... %v", sentinel, err)`, **When** standardized, **Then** all use `fmt.Errorf("context: %w", err)` with a single `%w` verb.
3. **Given** `go fix ./...` is run, **Then** the diff contains only auto-modernization changes and all tests pass.

---

### User Story 3 — Extract Magic Numbers to Named Constants (Priority: P2)

As a developer, I want all magic numbers and strings replaced with named constants so the codebase is self-documenting and values are easy to tune.

**Why this priority**: 5 high-severity magic number issues (H-004 through H-008) plus 3 low-severity ones reduce readability and make tuning error-prone.

**Independent Test**: Can be verified by `grep -rn 'magic\|30000\|"15:04"' internal/` returning zero hits in non-test files, and `golangci-lint run` with `goconst` enabled reporting no new violations.

**Acceptance Scenarios**:

1. **Given** `method_wait.go:31` uses `timeoutMs = 30000`, **When** extracted, **Then** a named constant `defaultWaitTimeoutMs` is defined and used.
2. **Given** `eventbridge.go:52` uses `make(chan Event, 64)`, **When** extracted, **Then** a named constant `eventChannelBuffer` is defined with a documenting comment.
3. **Given** `ratelimiter.go:160` uses `"15:04"`, **When** extracted, **Then** a named constant `timeFormatHHMM` or equivalent is defined.

---

### User Story 4 — Strengthen Linter Configuration (Priority: P3)

As a developer, I want the golangci-lint configuration expanded with 14 additional linters so that future code automatically meets higher quality standards.

**Why this priority**: Linters catch issues at write-time rather than audit-time. The 14 recommended additions (7 Tier-1: modernize, bodyclose, noctx, sqlclosecheck, wrapcheck, errorlint, musttag; 3 Tier-2: nilnil, gocognit, goconst; 4 Tier-3: exhaustive, intrange, perfsprint, fatcontext) are validated against 4 community golden configs (maratori, golangci-lint self, Brandur, olegk.dev).

**Independent Test**: Can be verified by running `golangci-lint run` with the updated config — zero new violations (existing issues either fixed or selectively suppressed with justification).

**Acceptance Scenarios**:

1. **Given** the new linters are added to `.golangci.yml`, **When** `golangci-lint run` executes, **Then** it exits 0 with no suppressed violations lacking justification.
2. **Given** `wrapcheck` is enabled with `ignorePackageGlobs: ["fmt"]` to avoid the documented errorlint conflict (golangci-lint #2238), **When** an adapter returns an unwrapped external error, **Then** the linter flags it without contradicting errorlint.
3. **Given** `exhaustive` is enabled with `default-signifies-exhaustive: true`, **When** a switch on `domain.Action` misses a case without a default, **Then** the linter flags it.

---

### User Story 5 — Add Fuzz Targets and Benchmarks (Priority: P3)

As a developer, I want fuzz targets for domain types and benchmarks for hot paths so the codebase has property-based testing and a performance baseline.

**Why this priority**: `FuzzJIDParse` gives free Scorecard Fuzzing credit (+10 points). Benchmarks for `RateLimiter.Allow()` and socket fan-out establish a regression baseline.

**Independent Test**: Can be verified by running `go test -fuzz=FuzzJIDParse ./internal/domain/ -fuzztime=10s` without crashes, and `go test -bench=. ./internal/app/` producing benchmark output.

**Acceptance Scenarios**:

1. **Given** `FuzzJIDParse` is added with a seed corpus, **When** run for 30 seconds, **Then** no crashes occur and the round-trip invariant holds for all discovered inputs.
2. **Given** `BenchmarkRateLimiterAllow` is added, **When** run, **Then** it produces stable ns/op results.

---

### User Story 6 — Fix Medium-Severity Code Smells (Priority: P3)

As a developer, I want medium-severity issues (nested ifs, os.Exit in handlers, missing t.Helper, inconsistent patterns) fixed so the codebase consistently follows the line-of-sight rule and Go testing idioms.

**Why this priority**: These are readability and testability improvements. The `os.Exit` pattern in CLI handlers (M-021) is the most impactful — it prevents CLI integration testing.

**Independent Test**: Can be verified by `gocognit` reporting no function above threshold 20, and `os.Exit` calls existing only in `main()` or explicitly justified test helpers.

**Acceptance Scenarios**:

1. **Given** `ratelimiter.go:164-170` has 3-level nesting, **When** the new-recipient check is extracted to a helper, **Then** max nesting is 2 levels.
2. **Given** CLI handlers call `os.Exit` directly, **When** refactored to return errors, **Then** only `main()` calls `os.Exit` and CLI commands are testable via `testscript`.
3. **Given** `dispatcher_test.go:17` is missing `t.Helper()`, **When** added, **Then** test failure output points to the calling test, not the helper.

---

### User Story 7 — SQLite & Audit Security Hardening (Priority: P2)

As a developer, I want SQLite security PRAGMAs added and audit log tamper detection implemented so the daemon meets OWASP A09:2025 and SQLite's "Defense Against the Dark Arts" recommendations.

**Why this priority**: The `mmap_size(268435456)` in `sqlitehistory/store.go:70` directly contradicts SQLite's official security guidance. Audit log lacks cryptographic integrity. These are security gaps, not just code quality.

**Independent Test**: Can be verified by querying `PRAGMA trusted_schema` returning 0, `PRAGMA cell_size_check` returning 1, and `wa audit verify` validating the HMAC chain.

**Acceptance Scenarios**:

1. **Given** `sqlitehistory/store.go` sets `mmap_size(268435456)`, **When** the security PRAGMA review is applied, **Then** `mmap_size` is set to 0, `trusted_schema=OFF`, and `cell_size_check=ON` are added to both session.db and messages.db open paths.
2. **Given** the audit log has no tamper detection, **When** HMAC hash chains are added, **Then** each JSON-lines record includes an `hmac` field and `wa audit verify` walks the log checking each HMAC.
3. **Given** audit records lack a `source` field, **When** added, **Then** every audit record identifies the originating component (e.g., `socket-rpc`, `allowlist-reload`).

---

### Edge Cases

- What happens when `golangci-lint run` with new linters reports violations in generated code? Exclude generated files via `skip-files` in config.
- What happens when `go fix` makes changes that break compilation? Run `go build ./...` immediately after and revert any breaking auto-fixes.
- What happens when splitting a god struct changes the adapter's public API? Maintain backward compatibility by keeping the top-level `Adapter` type as a facade that delegates to sub-structs.
- What happens when `mmap_size=0` degrades FTS5 query performance? Benchmark before/after; if >2x regression, document the security/performance trade-off and let the user choose via config.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: All 4 critical issues (C-001 through C-004) MUST be fixed with regression tests.
- **FR-002**: All 17 high-severity issues MUST be either fixed or explicitly deferred with documented justification.
- **FR-003**: Error wrapping MUST use a single `%w` verb per `fmt.Errorf` call — no `%w + %v` mixing.
- **FR-004**: `go fix ./...` MUST be run and its changes included.
- **FR-005**: `errors.AsType` MUST replace `errors.As` in `internal/app/errors.go`.
- **FR-006**: All magic numbers identified in H-004 through H-008 and L-001 through L-003 MUST be extracted to named constants.
- **FR-007**: `.golangci.yml` MUST be updated with at minimum 14 linters: 7 Tier-1 (modernize, bodyclose, noctx, sqlclosecheck, wrapcheck, errorlint, musttag), 3 Tier-2 (nilnil, gocognit, goconst), 4 Tier-3 (exhaustive, intrange, perfsprint, fatcontext). The wrapcheck/errorlint conflict (golangci-lint #2238) MUST be resolved via `ignorePackageGlobs: ["fmt"]`. The `run.go` version MUST be updated from `"1.22"` to `"1.26"` to activate all modernize analyzers.
- **FR-008**: At least one fuzz target (`FuzzJIDParse`) MUST be added with a committed seed corpus under `testdata/fuzz/`. A nightly CI workflow MUST run fuzz targets with `-fuzztime=2m`.
- **FR-009**: At least one benchmark (`BenchmarkRateLimiterAllow`) MUST be added. Benchmarks MUST be run before and after Phase 1 god-struct refactoring to catch performance regressions.
- **FR-010**: All existing tests MUST continue to pass (`go test -race ./...` exits 0).
- **FR-011**: `golangci-lint run` MUST exit 0 with the updated config. golangci-lint MUST be pinned at ≥v2.6.0 in CI.
- **FR-012**: No new `//nolint` suppressions without a justifying comment.
- **FR-013**: `slog.LogValuer` MUST be implemented on `domain.JID`, `domain.Message` (truncated body, redact content), and `domain.Session` (redact sensitive fields).
- **FR-014**: `PRAGMA trusted_schema=OFF` and `PRAGMA cell_size_check=ON` MUST be added to both session.db and messages.db open paths. `mmap_size` in `sqlitehistory/store.go` MUST be set to 0 per SQLite's security guidance, with a benchmark documenting the FTS5 performance impact.
- **FR-015**: Audit log records MUST include an HMAC hash chain field for tamper detection and a `source` field identifying the originating component, per OWASP A09:2025.
- **FR-016**: CI MUST run `go mod verify` and set `GOFLAGS=-mod=readonly` to prevent silent dependency tampering.
- **FR-017**: Error wrapping standardization (FR-003) MUST document `errors.Join` as the pattern for aggregating independent errors (cleanup, validation), distinct from single-`%w` context wrapping. The `errors.Unwrap()` nil-return gotcha with `errors.Join` MUST be documented in a code comment.
- **FR-018**: `go fix ./...` MUST be added as a recurring `lefthook` pre-commit hook (not just a one-time run) to catch LLM-generated old-style patterns on every toolchain upgrade.
- **FR-019**: The `os.Exit` refactoring in CLI handlers (M-021) is elevated to P2 priority. Community consensus (gh CLI, GoReleaser, Cobra issues #914/#2124) confirms it is a testability prerequisite, not an optional cleanup.
- **FR-020**: Coverage thresholds MUST be enforced: domain+app ≥90%, adapters ≥50%, total ≥70%. Use `vladopajic/go-test-coverage` or equivalent in CI.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Critical issue count drops from 4 to 0 (verified by re-running the audit agent swarm).
- **SC-002**: High-severity issue count drops from 17 to ≤3 (deferred items documented).
- **SC-003**: `golangci-lint run` exits 0 with ≥24 linters enabled (up from 10).
- **SC-004**: `go test -race ./...` exits 0 with ≥1 fuzz target and ≥1 benchmark present.
- **SC-005**: No function reports cognitive complexity >20 under `gocognit`.
- **SC-006**: Error wrapping is consistent: `grep -rn '%w.*%v\|%v.*%w' internal/ cmd/` returns 0 matches.
- **SC-007**: The `problems.md` and `research.md` files serve as the audit trail for all decisions made.
- **SC-008**: `PRAGMA trusted_schema` returns 0 and `PRAGMA cell_size_check` returns 1 on both databases.
- **SC-009**: Coverage thresholds pass: domain+app ≥90%, total ≥70%.
- **SC-010**: `os.Exit` calls exist only in `main()` functions or explicitly justified test helpers (verified by `grep -rn 'os.Exit' cmd/`).

## Assumptions

- The refactoring preserves all existing behavior — no functional changes, only structural improvements.
- `golangci-lint` v2.6+ is available (required for `modernize` linter).
- Go 1.26.1 toolchain is pinned in `go.mod` (already the case).
- The whatsmeow adapter and socket server god-struct refactors can be done without changing external-facing APIs.
- Medium and low severity issues not explicitly required by FR-001 through FR-013 may be addressed opportunistically but are not blockers.
