# Spec Quality Checklist: Application Use Cases

**Purpose**: Validate quality, testability, and completeness of spec.md for feature 005.
**Created**: 2026-04-09
**Feature**: [spec.md](../spec.md)

## Content Quality

- [X] CHK001 Spec focuses on WHAT (behavior) not HOW (implementation). No Go package names in FRs.
- [X] CHK002 Every FR is a single testable statement (one MUST per bullet).
- [X] CHK003 Scope boundary is explicit: use case layer only, composition root deferred to 006.
- [X] CHK004 User stories explain WHY (operator safety, account protection, operational visibility).
- [X] CHK005 No vague qualifiers without thresholds — all caps have numbers (2/s, 30/min, 1000/day, 25%/50%/100%).

## Requirement Completeness

- [X] CHK006 All five user stories have Given/When/Then acceptance scenarios.
- [X] CHK007 Each user story has an "Independent Test" description testable with in-memory fakes.
- [X] CHK008 Edge cases cover: nil params, unknown method, concurrent sends, EventStream errors, audit failures, rate limiter reset, already-paired, wait timeout.
- [X] CHK009 Safety pipeline order is specified (FR-009): parse → allowlist → rate limit → port call → audit.
- [X] CHK010 Error code mapping is explicit (FR-039): 7 domain errors → 7 JSON-RPC codes.
- [X] CHK011 Warmup ramp thresholds are exact: 25% at 0-7 days, 50% at 7-14, 100% at 14+.
- [X] CHK012 Audit logging is specified: one entry per outbound action, denied requests include reason.
- [X] CHK013 Event bridge type mapping is explicit (FR-033): 4 domain event types → 4 string names.

## Requirement Clarity

- [X] CHK014 "Default deny" allowlist is unambiguous: every outbound action is checked; read-only methods are exempt.
- [X] CHK015 Rate limiter scope is clear: server-wide (not per-connection), three independent buckets.
- [X] CHK016 Which methods go through safety pipeline vs. which are exempt is listed explicitly (FR-009..FR-013, FR-017, FR-024).
- [X] CHK017 The `wait` method semantics are distinct from `subscribe` (synchronous blocking vs. streaming).
- [X] CHK018 Deferred methods (`allow`, `panic`) are listed in both Edge Cases and Out of Scope.

## Requirement Consistency

- [X] CHK019 FR-039 error codes match the table in feature 004's contracts/wire-protocol.md (-32011..-32018 range).
- [X] CHK020 The 8 port interfaces referenced in FR-002 match ports.go exactly (MessageSender, EventStream, ContactDirectory, GroupManager, SessionStore, Allowlist, AuditLog, HistoryStore).
- [X] CHK021 Rate limit defaults (2/s, 30/min, 1000/day) match CLAUDE.md §Safety exactly.
- [X] CHK022 Warmup percentages (25/50/100 at day boundaries 0/7/14) match CLAUDE.md §Safety exactly.
- [X] CHK023 The Dispatcher interface shape (Handle + Events) matches feature 004's dispatcher.go.

## Measurability

- [X] CHK024 Every success criterion has a numeric target or binary verifiable condition.
- [X] CHK025 SC-003 (rate limiter rejection) is verifiable with a deterministic counting test.
- [X] CHK026 SC-004 (warmup ramp) is verifiable with a table-driven test and mocked clock.
- [X] CHK027 SC-007 (audit entry count) is verifiable by counting entries in the in-memory fake.
- [X] CHK028 SC-010 (depguard) is verifiable by running golangci-lint.

## Scope Coverage

- [X] CHK029 Every FR maps to at least one user story by topic.
- [X] CHK030 Every user story is independently testable with in-memory fakes.
- [X] CHK031 No FR depends on a real WhatsApp connection — all tests use fakes.
- [X] CHK032 Dependencies are listed and none blocks this feature's implementation.

## Ambiguities

- [X] CHK033 No `[NEEDS CLARIFICATION]` markers remain.
- [X] CHK034 No two FRs contradict each other.
- [X] CHK035 The `markRead` port shape ambiguity is documented in Assumptions and deferred to plan phase.

## Notes

- All 35 items pass on first draft.
- One open design question (markRead port shape) is documented in Assumptions and will be resolved during `/speckit:plan`.
