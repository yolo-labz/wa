# Spec Quality Checklist: Multi-Profile Support

**Purpose**: Validate spec.md quality, testability, and completeness for feature 008.
**Created**: 2026-04-11
**Feature**: [spec.md](../spec.md), [research.md](../research.md), [refactor.md](../refactor.md)

## Content Quality

- [X] CHK001 Spec focuses on observable user behavior (profile selection, isolation, migration) not implementation mechanics.
- [X] CHK002 Every FR is a single testable statement with a MUST/SHOULD marker.
- [X] CHK003 Scope boundary is explicit: per-profile daemon model, not multi-tenant. Rejected alternatives listed in Out of Scope.
- [X] CHK004 User stories explain WHY for each priority (upgrade safety, core value, UX polish, convenience, persistence, lifecycle management).
- [X] CHK005 No vague qualifiers without thresholds — all SCs have numeric targets or verifiable binary conditions.

## Requirement Completeness

- [X] CHK006 All six user stories have Given/When/Then acceptance scenarios.
- [X] CHK007 Each user story has an Independent Test that can run against fakes or in CI (with a documented exception for US2 which requires a real pair).
- [X] CHK008 Edge cases cover: name-too-long, invalid regex, reserved names, `default` allowlist, daemon race, pre-existing target dir, interrupted migration, nonexistent profile switch, invalid regex, pair HTML collision, shared cache rationale, rm running daemon.
- [X] CHK009 Migration sequence is fully specified: flock scope, file list, audit entry, idempotency, rollback conditions.
- [X] CHK010 Precedence order for profile resolution is explicit: flag → env → file → `default`.
- [X] CHK011 Backward compatibility is specified for all three layers: paths, CLI surface, service installation.
- [X] CHK012 The pre-existing warmup timestamp bug (FR-032) is documented with its fix bundled in this feature.
- [X] CHK013 Reserved name list is explicit (FR-003) with `default` explicitly allowed.
- [X] CHK014 Schema version file is specified so future refactors have a versioning hook.

## Requirement Clarity

- [X] CHK015 Profile name regex is exact: `^[a-z][a-z0-9-]{0,30}[a-z0-9]$` — no interpretation needed.
- [X] CHK016 All XDG paths are specified with `<profile>` segment: data/config/state under subdir, runtime socket flat, cache shared.
- [X] CHK017 systemd template vs per-profile unit is decided: template (one `wad@.service` file + N enabled instances).
- [X] CHK018 launchd multi-instance is decided: one plist per profile (no template available).
- [X] CHK019 CLI flag precedence over env var over active-profile file is explicit.
- [X] CHK020 `wa profile rm` hard constraints are explicit: refuses active, only, running.
- [X] CHK021 Migration's atomicity guarantee is specified (flock + rename atomicity).
- [X] CHK022 Shell completion source is specified: `filepath.Glob($XDG_DATA_HOME/wa/*/session.db)`.

## Requirement Consistency

- [X] CHK023 Exit codes in FR match CLAUDE.md §Output schema (0, 10, 11, 12, 64, 78) exactly. FR-039 uses 78 (config error), FR-041 uses 10 (service unavailable) — both consistent with existing table.
- [X] CHK024 Rate limiter defaults (2/s, 30/min, 1000/day) are unchanged per profile — consistent with feature 005 FR-014.
- [X] CHK025 Warmup ramp (25/50/100% at days 0/7/14) is unchanged per profile — consistent with feature 005 FR-019.
- [X] CHK026 Socket path conventions (flat filename, `.lock` sibling) are consistent with feature 004's contracts/socket-path.md.
- [X] CHK027 Audit actions (`migrate`, `grant`, `revoke`, `send`, `pair`) match `internal/domain/audit.go` — the `migrate` action name is new but follows the existing `AuditAction` enum pattern.
- [X] CHK028 The `Actor` field format `wad:<profile>` is consistent with the existing `actor: "dispatcher"` convention.

## Measurability

- [X] CHK029 Every SC has a numeric target or binary verifiable condition.
- [X] CHK030 SC-001 (zero data loss) is verifiable by before/after state diff.
- [X] CHK031 SC-003 (no cross-contamination) is verifiable by dual-instance bench test.
- [X] CHK032 SC-006 (migration <200ms for 100MB) is verifiable with a table-driven test.
- [X] CHK033 SC-012 (zero port interface changes) is verifiable by `git diff internal/app/ports.go` being empty.

## Scope Coverage

- [X] CHK034 Every FR maps to at least one user story.
- [X] CHK035 Every user story has at least one corresponding FR.
- [X] CHK036 The dependency list (features 002–007 + hotfix PR #8) is accurate.
- [X] CHK037 Out of Scope is explicit: multi-tenant daemon, per-profile tuning, cross-profile ops, profile sync, encryption at rest, web UI, Windows.
- [X] CHK038 The two-swarm research approach is documented in Notes so reviewers understand the provenance of the decisions.

## Constitution Alignment

- [X] CHK039 Principle I (hexagonal core) — no port interface changes required (SC-012).
- [X] CHK040 Principle II (daemon owns state) — each profile is a separate daemon with exclusive state ownership (FR-030, FR-031).
- [X] CHK041 Principle III (safety first) — each profile has its own full safety pipeline; no shared state (FR-030, FR-032).
- [X] CHK042 Principle IV (CGO forbidden) — no new dependencies; existing stack unchanged.
- [X] CHK043 Principle V (spec-driven with citations) — research.md has D1-D8 with primary-source URLs.
- [X] CHK044 Principle VI (port-boundary fakes) — tests continue to use existing memory adapters without changes.
- [X] CHK045 Principle VII (conventional commits) — inherited from governance setup in feature 001.

## Ambiguities and Conflicts

- [X] CHK046 No `[NEEDS CLARIFICATION]` markers in the spec.
- [X] CHK047 No two FRs contradict each other.
- [X] CHK048 The `default` profile is explicitly NOT reserved (clarified in FR-003 and Edge Cases).
- [X] CHK049 The bundled warmup bug fix is clearly scoped as "prerequisite inside feature 008", not a separate feature.

## Notes

- All 49 items pass on first draft.
- The research and refactor documents make this the best-prepared feature in the project — every architectural decision traces to either codebase analysis (refactor.md) or primary-source research (research.md D1–D8).
- The only non-trivial ambiguity resolved during spec writing was whether `default` is a reserved name. The swarm 2.4 agent initially suggested reserving it, but the correction is: `default` IS the canonical default profile name, so it must be valid. This is now explicit in FR-003 and Edge Cases.
