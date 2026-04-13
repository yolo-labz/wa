# Architecture, Security & Modernization Checklist: Code Quality Audit (016)

**Purpose**: Validate that the spec, plan, and research artifacts follow modern Go trends, best architectural decisions, and research-based security practices (2025-2026)
**Created**: 2026-04-13
**Feature**: [spec.md](../spec.md) | [plan.md](../plan.md) | [research.md](../research.md)
**Method**: 5-agent deep research swarm covering refactoring patterns, security, linter architecture, hexagonal patterns, and testing modernization

---

## Architectural Decision Quality

- [x] CHK001 Is the god-struct decomposition strategy (facade + sub-structs) documented with a rationale for why sub-structs remain in the same package vs. separate packages? **RESOLVED**: plan.md Phase 1.2 now cites Three Dots Labs wild-workouts and Sam Smith's hexagonal Go guide, with explicit rationale that extracting sub-packages would force shared fields to become exported. [plan.md Phase 1.2-1.3] [Clarity]

- [x] CHK002 Does the plan specify a maximum field count per sub-struct after the split, or does it use a cognitive-load-based heuristic instead? **RESOLVED**: plan.md Phase 1.2 replaced "≤7 fields" with SRP heuristic from "Exploding Large Go Structs" by Breaking Computer, noting no Go community consensus exists on a hard field limit. [plan.md Phase 1.2] [Clarity]

- [x] CHK003 Is the EventBridge deadlock fix (C-001) specified as the copy-under-lock pattern? **RESOLVED**: plan.md Phase 1.1 now explicitly names the copy-under-lock pattern with citations (Watermill gochannel/pubsub.go, NATS Go client, Eli Bendersky's PubSub pattern). Also mentions go-deadlock as a development validation tool. [plan.md Phase 1.1] [Completeness]

- [x] CHK004 Does the plan address whether the Dispatcher's 15-field/16-method structure should be refactored to the Three Dots Labs per-handler CQRS pattern, or is the current mediator pattern explicitly accepted at current scale? **RESOLVED**: plan.md Phase 6.6 explicitly accepts the current pattern at current scale (8 methods) and documents Three Dots Labs per-handler CQRS as the migration path if method count exceeds ~15. [plan.md Phase 6.6] [Ambiguity]

- [x] CHK005 Is the composition root refactoring (M-023) specified with named phase functions (`initConfig`, `openStores`, `wireDispatcher`, `serve`) as recommended by the gh CLI and GoReleaser patterns? **RESOLVED**: plan.md Phase 6.3 now specifies the exact function signatures with lifecycle/cleanup pattern (each returns struct with Close() error). [plan.md Phase 6.3] [Clarity]

- [x] CHK006 Does the plan document why Wire/Fx dependency injection frameworks are rejected for the composition root, with the manual DI alternative cited? **RESOLVED**: plan.md Phase 6.3 now cites Redowan and Leapcell, documents Wire (code gen overkill for ~10 deps) and Fx (runtime DI, ~3.8ms overhead) as rejected alternatives. [plan.md Phase 6.3] [Gap]

- [x] CHK007 Is the adapter testing strategy for god-struct splits defined? **RESOLVED**: plan.md Phase 1.2 now specifies: porttest/ contract tests as primary safety net (Watermill pattern), unit tests for extracted sub-struct methods, DO NOT move existing test files, run benchmarks before/after. [plan.md Phase 1.2] [Gap]

---

## Security Requirements Quality

- [x] CHK008 Does the spec or plan address the `mmap_size(268435456)` setting in `sqlitehistory/store.go:70` that directly conflicts with SQLite's official security guidance? **RESOLVED**: spec.md adds FR-014 requiring mmap_size=0, plan.md Phase 6.4 details the change with benchmark requirement. User Story 7 added for SQLite & audit security hardening. [spec.md FR-014, plan.md Phase 6.4] [Gap]

- [x] CHK009 Are SQLite security PRAGMAs (`trusted_schema=OFF`, `cell_size_check=ON`) specified as requirements? **RESOLVED**: spec.md FR-014 requires both PRAGMAs. plan.md Phase 6.4 specifies adding them to both store open paths. [spec.md FR-014, plan.md Phase 6.4] [Gap]

- [x] CHK010 Does the audit log specification include tamper detection requirements? **RESOLVED**: spec.md FR-015 requires HMAC hash chain. plan.md Phase 6.5 specifies HMAC-SHA256 per record with key derived from daemon session identity. User Story 7 acceptance scenario covers this. [spec.md FR-015, plan.md Phase 6.5] [Gap]

- [x] CHK011 Are audit log fields complete per OWASP Logging Cheat Sheet requirements? **RESOLVED**: plan.md Phase 6.5 adds `source` field and explicit `event_time` vs `log_time` distinction. [plan.md Phase 6.5] [Gap]

- [ ] CHK012 Does the rate limiter specification address inter-message jitter distribution? **DEFERRED**: This is a functional behavior change to the rate limiter, not a code quality refactoring. Tracked as a future feature requirement for the safety pipeline. The baileys-antiban gaussian jitter research is documented in research.md for when this work is scoped. [spec.md FR requirements] [Gap — out of scope for refactoring feature]

- [ ] CHK013 Is the new-contact penalty specified? **DEFERRED**: Same rationale as CHK012. Functional rate limiter enhancement, not refactoring. [spec.md FR requirements] [Gap — out of scope]

- [ ] CHK014 Is identical-message deduplication specified? **DEFERRED**: Same rationale as CHK012. Functional addition, not refactoring. [spec.md FR requirements] [Gap — out of scope]

- [x] CHK015 Does the plan specify `go mod verify` in CI? **RESOLVED**: spec.md FR-016 and plan.md Phase 5.6 both specify `go mod verify` in CI. [plan.md Phase 5.6] [Gap]

- [x] CHK016 Is `GOFLAGS=-mod=readonly` specified for CI? **RESOLVED**: spec.md FR-016 and plan.md Phase 5.6 both specify this. [plan.md Phase 5.6] [Gap]

- [x] CHK017 Does the unix socket security section acknowledge the macOS trust boundary? **RESOLVED**: This is already documented in CLAUDE.md's Filesystem layout section ("Permissions: `0700` on every per-profile subdirectory") and research.md now notes the macOS-specific nuance from the security research agent. [research.md] [Clarity — already documented]

---

## Linter Configuration Completeness

- [x] CHK018 Does the plan address the wrapcheck/errorlint conflict? **RESOLVED**: plan.md Phase 4.4 now specifies `wrapcheck.ignorePackageGlobs: ["fmt"]` citing golangci-lint issue #2238. spec.md FR-007 updated. [plan.md Phase 4.4] [Conflict]

- [x] CHK019 Are the 4 additional high-value linters included? **RESOLVED**: plan.md Phase 4.3 adds `exhaustive`, `intrange`, `perfsprint`, `fatcontext` with rationale from maratori golden config validation. spec.md FR-007 updated to require 14 linters. [plan.md Phase 4.3] [Gap]

- [x] CHK020 Does the plan specify updating `run.go` from `"1.22"` to `"1.26"`? **RESOLVED**: plan.md Phase 4.4 explicitly specifies this update. [plan.md Phase 4.4] [Gap]

- [x] CHK021 Is golangci-lint minimum version pinned at v2.6.0+ in CI? **RESOLVED**: plan.md Phase 4.4 specifies the pin. spec.md FR-011 updated. [plan.md Phase 4.4] [Completeness]

- [x] CHK022 Are `wrapcheck.ignorePackageGlobs` settings specified? **RESOLVED**: plan.md Phase 4.4 adds `ignorePackageGlobs: ["fmt"]` citing Brandur and maratori configs. [plan.md Phase 4.4] [Completeness]

- [x] CHK023 Is the `goconst` test-file exclusion explicitly specified as a linter setting? **RESOLVED**: plan.md Phase 4.4 specifies `goconst.ignore-tests: true` as a setting, noting 2 of 4 golden configs skip goconst entirely due to noise. [plan.md Phase 4.4] [Clarity]

---

## Testing Strategy Completeness

- [x] CHK024 Does the plan specify fuzz testing in CI? **RESOLVED**: plan.md Phase 5.1 adds nightly CI workflow with `-fuzztime=2m`. spec.md FR-008 updated. [plan.md Phase 5.1] [Gap]

- [x] CHK025 Are coverage thresholds defined per layer? **RESOLVED**: spec.md FR-020 and plan.md Phase 5.5 specify: domain+app ≥90%, adapters ≥50%, cmd ≥40%, total ≥70% using vladopajic/go-test-coverage. SC-009 added. [spec.md FR-020, plan.md Phase 5.5] [Gap]

- [ ] CHK026 Does the plan address property-based testing (`rapid` library)? **DEFERRED**: `rapid` is a new dependency addition beyond the scope of a code quality refactoring. Research findings documented in research.md for future adoption. Native fuzz targets (FuzzJIDParse) cover the immediate need. [plan.md Phase 5] [Gap — future enhancement]

- [x] CHK027 Is `slog.LogValuer` specified for all relevant domain types? **RESOLVED**: spec.md FR-013 expanded to include JID, Message (truncated+redacted), and Session (redact sensitive). plan.md Phase 5.3 updated with all three. [spec.md FR-013, plan.md Phase 5.3] [Completeness]

- [x] CHK028 Does the plan specify running benchmarks before AND after refactoring? **RESOLVED**: plan.md Phase 1.2 now specifies "Run benchmarks before and after to catch performance regressions". Verification strategy Phase 1 updated. [plan.md Phase 1.2] [Consistency]

- [x] CHK029 Are synctest expansion targets explicitly listed? **RESOLVED**: plan.md Phase 5.4 now lists specific targets: EventBridge timeout behavior, subscribe/wait timeout logic, reconnect state transitions. [plan.md Phase 5.4] [Completeness]

---

## Error Handling & Modernization Clarity

- [x] CHK030 Does the error wrapping standardization address `errors.Join`? **RESOLVED**: plan.md Phase 2.3 now documents `errors.Join` as the distinct pattern for aggregating independent errors, with the `errors.Unwrap()` nil-return gotcha cited (Ian Lewis TIL, golang/go#57358). spec.md FR-017 added. [plan.md Phase 2.3, spec.md FR-017] [Clarity]

- [x] CHK031 Is `go fix` adoption specified with the `go.mod` version prerequisite? **RESOLVED**: plan.md Phase 2.1 now includes the prerequisite check. [plan.md Phase 2.1] [Completeness]

- [x] CHK032 Does the plan specify `go fix` as a recurring hook? **RESOLVED**: plan.md Phase 2.1 specifies adding `go fix ./...` as a `lefthook` pre-push hook. spec.md FR-018 added. [plan.md Phase 2.1, spec.md FR-018] [Gap]

- [x] CHK033 Is `new(value)` syntax adoption scoped? **RESOLVED**: plan.md Phase 2.2 now targets JSON-RPC result structs where optional pointer fields use `ptr[T]` helpers. [plan.md Phase 2.2] [Clarity]

---

## Specification Consistency

- [x] CHK034 Does FR-007 conflict with research linter recommendations? **RESOLVED**: spec.md FR-007 updated to require 14 linters (7 Tier-1 + 3 Tier-2 + 4 Tier-3). [spec.md FR-007] [Conflict]

- [x] CHK035 Does SC-003 align with actual linter count? **RESOLVED**: SC-003 updated to "≥24 linters enabled" (10 existing + 14 new). [spec.md SC-003] [Consistency]

- [x] CHK036 Does Phase 6 "opportunistic" status conflict with FR-002? **RESOLVED**: Phase 6 items that are high-impact (os.Exit, composition root) are either elevated (os.Exit → Phase 2.5) or committed (composition root Phase 6.3 is no longer "opportunistic"). FR-002's "deferred with documented justification" is satisfied. [spec.md FR-002 vs plan.md Phase 6] [Consistency]

- [x] CHK037 Is os.Exit refactoring correctly categorized? **RESOLVED**: Elevated from P3/Phase 6 to P2/Phase 2.5. spec.md FR-019 added. SC-010 added. [spec.md FR-019, plan.md Phase 2.5] [Ambiguity]

---

## Cross-Cutting Gaps

- [x] CHK038 Does any artifact address `go-deadlock`? **RESOLVED**: plan.md Phase 1.1 now mentions go-deadlock as a development validation tool for the C-001 fix. [plan.md Phase 1.1] [Gap]

- [ ] CHK039 Is porttest/ specified to gain fuzz-based contract tests via `rapid.MakeFuzz`? **DEFERRED**: Same rationale as CHK026 — `rapid` is a new dependency. [Gap — future enhancement]

- [ ] CHK040 Does quickstart.md include a security-specific check? **DEFERRED**: quickstart.md will be updated after implementation when the actual PRAGMA verification command is known. Tracked. [quickstart.md] [Coverage — update post-implementation]

- [x] CHK041 Is there a requirement for `PRAGMA quick_check` on SQLite open? **RESOLVED**: plan.md Phase 6.4 specifies `PRAGMA quick_check` on startup for session.db. [plan.md Phase 6.4] [Gap]

---

## Notes

- **Total items**: 41
- **Resolved**: 35/41 (85%)
- **Deferred with justification**: 5/41 (12%) — CHK012-014 (rate limiter jitter/penalty/dedup are functional changes, not refactoring), CHK026/039 (`rapid` library is a new dependency addition), CHK040 (quickstart update post-implementation)
- **Unresolvable**: 0
- **Traceability**: 41/41 items (100%) have resolution status with artifact references
- **Research sources**: 5 deep-research agents covering 60+ primary sources
- **Artifacts updated**: spec.md (7 new FRs: FR-014 through FR-020, 3 new SCs, 1 new User Story), plan.md (all 6 phases updated with citations, rationale, and gap fixes)
