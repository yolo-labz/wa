# Architecture & Modernity Checklist: Application Use Cases

**Purpose**: Validate that the architectural decisions, library choices, and design patterns in this feature are correct, modern, and internally consistent — "unit tests for the English."
**Created**: 2026-04-09
**Feature**: [spec.md](../spec.md), [plan.md](../plan.md), [research.md](../research.md)

## Hexagonal Boundary Integrity

- [X] CHK001 Is the import direction explicitly specified? Does the spec confirm `internal/app/` imports ONLY `internal/domain/` and stdlib — never `internal/adapters/*`? [Consistency] [Spec §Overview, Plan §Constitution I] — Yes: spec §Overview para 3, plan constitution row I, FR-042 adds mechanical enforcement via depguard.
- [X] CHK002 Is the `AppEvent` vs `socket.Event` split documented with the exact conversion mechanism (composition-root adapter goroutine)? [Clarity] [Research D2] — Yes: research D2 describes the 3-step solution, contracts/dispatcher-impl.md §Composition root adapter shows the exact adapter struct shape.
- [X] CHK003 Does the plan confirm that Go structural typing is NOT relied upon to satisfy `socket.Dispatcher` from `internal/app/` (since `Events()` returns different channel element types)? [Clarity] [Research D2] — Yes: D2 explicitly states the channel element type mismatch prevents structural satisfaction; the composition root adapter is required.
- [X] CHK004 Is the thin adapter in `cmd/wad` (feature 006) explicitly scoped — does the contract document its exact shape so feature 006 authors know what to build? [Completeness] [Contracts/dispatcher-impl.md §Composition root adapter] — Yes: the contract shows the `dispatcherAdapter` struct with `Handle` delegation and `Events()` goroutine conversion, estimated at ~20 LoC.
- [X] CHK005 Does the depguard rule for `internal/app/` also forbid imports from `internal/adapters/**`? [Gap] — **FIXED**: FR-042 added to spec.md requiring a new `app-no-adapters` depguard rule. Research D7 documents the rationale.
- [X] CHK006 Is the `MarkRead` addition to `MessageSender` documented as a cross-feature change with explicit justification for not creating a 9th port? [Completeness] [Research D3, Plan §Complexity Tracking] — Yes: D3 explains why a separate port is overengineered, plan §Complexity Tracking has the table row.

## Rate Limiter Design Quality

- [X] CHK007 Is the "wasted token" behavior documented as intentionally conservative? [Clarity] [Research D1] — Yes: D1 says "conservative (tighter than necessary), which is safe for anti-ban purposes."
- [X] CHK008 Are the three bucket defaults traceable to CLAUDE.md §Safety and consistent across all artifacts? [Consistency] — Yes: CLAUDE.md says "1-2/s, ~30, ~1000"; spec FR-014 says "2, 30, 1000"; data-model and contract match.
- [X] CHK009 Is the warmup multiplier specified as a pure function with exact thresholds? [Measurability] — Yes: FR-019..FR-022 specify exact day boundaries and percentages; contract/rate-limiter.md has the table.
- [X] CHK010 Is `burst = max(1, ...)` specified so burst never drops to zero? [Edge Cases] — Yes: contract/rate-limiter.md says "Burst values are `max(1, int(defaultBurst * multiplier))` — never zero."
- [X] CHK011 Is the decision to NOT persist rate limiter state documented with rationale? [Assumptions] — Yes: spec §Assumptions para 1.
- [X] CHK012 Is the decision to NOT make rate limits configurable documented as a scope decision? [Completeness] — Yes: spec §Out of Scope says "Configurable rate limits (v0.2)."

## Safety Pipeline Coherence

- [X] CHK013 Is the pipeline execution order specified as normative? [Clarity] — Yes: FR-009 uses "MUST execute...in this exact order" and contracts/dispatcher-impl.md shows the numbered sequence.
- [X] CHK014 Is it specified that audit entries are recorded for BOTH denials and successes? [Completeness] — **FIXED**: FR-036 updated to explicitly state "Audit entries MUST be recorded for BOTH denials...AND successes."
- [X] CHK015 Is the audit-on-denial ordering specified? [Clarity] — **FIXED**: FR-036 now says "recorded AFTER the pipeline decision but BEFORE the error/result is returned to the caller."
- [X] CHK016 Are gated and exempt method sets explicitly enumerated? [Completeness] — Yes: FR-009 lists gated (send, sendMedia, react, markRead); FR-013, FR-017, FR-024, FR-031 list exempt (status, groups, pair, wait).
- [X] CHK017 Is "no --force ever" traceable constitution→spec→contract? [Consistency] — Yes: constitution III → FR-018 ("no --force flag, no admin override") + FR-021 ("not configurable, no override") → contract/rate-limiter.md §No override.
- [X] CHK018 Is the distinction between ErrRateLimited and ErrWarmupActive documented? [Clarity] — Yes: data-model §Typed errors assigns distinct codes (-32013 vs -32014); contract/rate-limiter.md §Allow check explains: "ErrWarmupActive — a bucket was exhausted AND the warmup multiplier is < 1.0."

## Event Bridge & Wait Design

- [X] CHK019 Is the single-consumer invariant on EventStream.Next() preserved? [Consistency] — Yes: research D4 explicitly rejects option (b) "two competing consumers"; FR-032 (updated) confirms only the bridge reads Next().
- [X] CHK020 Is the fan-out mechanism specified in enough detail? [Completeness] — Yes: research D4 describes the waiter slice + mutex pattern; data-model §EventBridge lists the fields.
- [X] CHK021 Is the waiter channel overflow behavior specified? [Edge Cases] — **FIXED**: data-model §Waiter updated to document cap-1 behavior; spec §Edge Cases now includes "wait waiter channel overflow" with drop semantics.
- [X] CHK022 Is the `wait` timeout default and error documented? [Completeness] — Yes: FR-029 (30000ms default), FR-030 ("return a timeout error"), data-model §Typed errors maps it to ErrWaitTimeout / -32003.
- [X] CHK023 Is the bridge error-retry behavior specified? [Completeness] — Yes: FR-035 says "log and retry after 100ms backoff."
- [X] CHK024 Is the bridge shutdown sequence specified step by step? [Clarity] — Yes: FR-034 says "exit and close the Events() channel without leaking goroutines"; contracts/dispatcher-impl.md §Close has the numbered sequence.

## Tooling Modernity (Go 2026)

- [X] CHK025 Is `x/time/rate` confirmed as the standard Go token-bucket? [Modernity] — Yes: research D1 confirms it's the stdlib-adjacent standard; no stdlib promotion has occurred as of Go 1.25.
- [X] CHK026 Is `testing/synctest` considered for rate-limiter tests? [Modernity] — **FIXED**: research D6 added; plan §Testing updated to document synctest usage for rate-limiter/warmup tests (unlike feature 004 socket tests, no real I/O here).
- [X] CHK027 Is goleak wired into TestMain? [Modernity] — Yes: plan §Testing mentions goleak for `internal/app/`.
- [X] CHK028 Does the plan use slog? [Consistency] — Yes: plan §Technical Context inherits slog from feature 004; AppDispatcher has a `*slog.Logger` field in data-model.
- [X] CHK029 Are error types designed for errors.Is/errors.As? [Modernity] — Yes: data-model §Typed errors says they implement `codedError` interface; the pattern from feature 004's errors.go uses `errors.New` sentinels compatible with `errors.Is`.

## Requirement Completeness

- [X] CHK030 Is `pair`'s interaction with session store specified? [Completeness] — Yes: FR-025 says "If a session already exists, return typed already-paired error." This implies checking SessionStore.Load().
- [X] CHK031 Is empty-allowlist behavior documented? [Edge Cases] — **FIXED**: spec §Edge Cases now includes "Empty allowlist (no entries): Default deny means ALL sends are rejected...the operator must explicitly add JIDs."
- [X] CHK032 Is `react` empty-emoji semantics documented? [Completeness] — Yes: FR-007 routes through MessageSender.Send with domain.ReactionMessage; domain/message.go §ReactionMessage.Validate allows empty Emoji ("means remove the reaction").
- [X] CHK033 Is the method-not-found error path specified? [Clarity] — Yes: FR-003 says "Unknown methods MUST return an error that the socket adapter maps to -32601."
- [X] CHK034 Are `allow` and `panic` listed as method-not-found until feature 006? [Completeness] — **FIXED**: FR-041 added: "MUST NOT be registered in the method table. Requests...MUST return method-not-found."

## Consistency Across Artifacts

- [X] CHK035 Do error codes in data-model match feature 004's wire-protocol.md? [Consistency] — Yes: data-model codes -32011..-32018 match the "Reserved for feature 005 domain errors" block in feature 004's wire-protocol.md.
- [X] CHK036 Does the method table in contracts match the FR list? [Consistency] — Yes: contracts/dispatcher-impl.md has 8 rows (send, sendMedia, react, markRead, pair, status, groups, wait) matching FRs 005-031.
- [X] CHK037 Does the LOC budget align with the plan description? [Consistency] — Yes: data-model says ~1330; plan §Summary says "smaller than features 003 and 004" (which were ~2500 and ~3200).
- [X] CHK038 Is AppEvent.Payload type consistent with socket.Event.Payload? [Consistency] — Yes: both are `any` (Go 1.18+ alias for `interface{}`).
- [X] CHK039 Does quickstart.md reference correct paths? [Consistency] — Yes: quickstart step 3 references `go build ./internal/app/...`; step 4 references `go test -race ./internal/app/...` — matching plan §Project Structure.

## Notes

- All 39 items now pass.
- 7 items required fixes (CHK005, CHK014, CHK015, CHK021, CHK026, CHK031, CHK034) — spec, plan, research, and data-model were updated accordingly.
- The two highest-priority gaps (CHK005: depguard rule, CHK026: synctest for rate tests) are now fully addressed with new FRs and research D-blocks.
