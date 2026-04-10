# Spec Quality Checklist: Binaries and Composition Root

**Purpose**: Validate spec.md quality, testability, and completeness for feature 006.
**Created**: 2026-04-09
**Feature**: [spec.md](../spec.md)

## Content Quality

- [X] CHK001 Spec focuses on user-visible behavior (pair, send, allow, panic) not implementation.
- [X] CHK002 Every FR is a single testable statement.
- [X] CHK003 Scope boundary is explicit: composition root + CLI only; packaging (GoReleaser/launchd/Nix) deferred to 007.
- [X] CHK004 User stories explain WHY (first runnable daemon, allowlist mutability, security panic, observability, signal-safe shutdown, UX polish).
- [X] CHK005 No vague qualifiers without thresholds — SC-001 through SC-010 all have numeric targets.

## Requirement Completeness

- [X] CHK006 All six user stories have Given/When/Then acceptance scenarios.
- [X] CHK007 Every user story has an Independent Test description that works against fakes (no burner required except US1 manual path).
- [X] CHK008 Edge cases cover: missing dirs, empty allowlist, daemon not running, wrong-user client, malformed TOML, already-paired, mid-request SIGTERM, concurrent clients.
- [X] CHK009 Error code → exit code mapping is explicit (FR-015).
- [X] CHK010 Adapter close order on shutdown is specified (FR-033).
- [X] CHK011 Allowlist file-watcher + SIGHUP dual mechanism is specified (FR-022).
- [X] CHK012 The `Pairer` seam is documented as the ONLY port change in this feature (FR-029, Assumptions).
- [X] CHK013 The `dispatcherAdapter` is documented as implementing socket.Dispatcher via goroutine (FR-004).

## Requirement Clarity

- [X] CHK014 XDG paths are specified exactly (data/config/state/runtime) with darwin fallback for runtime (FR-006).
- [X] CHK015 The `allow add` → TOML write → reload cycle is specified with a 1s SLA (SC-005).
- [X] CHK016 `wa panic` local-first semantics are clear: always succeeds locally even if upstream unlink fails (FR-026).
- [X] CHK017 Signal handling uses `signal.NotifyContext` with SIGINT + SIGTERM (FR-031).
- [X] CHK018 The integration test is gated behind `//go:build integration` + `WA_INTEGRATION=1` (FR-036).

## Requirement Consistency

- [X] CHK019 Exit codes in FR-014/FR-015 match CLAUDE.md §Output schema exactly.
- [X] CHK020 The `socket.Path()` function referenced in FR-012 is the real symbol from feature 004.
- [X] CHK021 `app.Dispatcher.Events() <-chan app.Event` and `socket.Dispatcher.Events() <-chan socket.Event` mismatch is acknowledged and resolved via `dispatcherAdapter` (FR-004).
- [X] CHK022 Audit actions (AuditGrant, AuditRevoke, AuditPanic) match domain/audit.go existing constants.
- [X] CHK023 The shutdown order in FR-033 is consistent with construction order in FR-002 (reverse).

## Measurability

- [X] CHK024 Every SC has a numeric target or binary verifiable condition.
- [X] CHK025 SC-002 (status <100ms), SC-003 (startup <500ms), SC-004 (shutdown <2s), SC-006 (panic <500ms) are wall-clock measurable.
- [X] CHK026 SC-005 (allow reload <1s) is verifiable by a deterministic test.
- [X] CHK027 SC-010 (golangci-lint clean) is verifiable by running the linter.

## Scope Coverage

- [X] CHK028 Every FR maps to at least one user story.
- [X] CHK029 Every user story is independently testable (with burner-phone caveat on US1 manual path).
- [X] CHK030 The dependency list (features 002–005) is accurate.
- [X] CHK031 Out of Scope explicitly lists GoReleaser, launchd/systemd, rcodesign, completions, doctor, multi-profile, self-update, metrics, Windows.

## Ambiguities

- [X] CHK032 No `[NEEDS CLARIFICATION]` markers remain.
- [X] CHK033 No two FRs contradict each other (e.g., no "close whatsmeow before dispatcher" vs reverse).

## Notes

- All items pass on first draft.
- FR-029 (`Pairer` seam) is the only new port interface in this feature. Research phase will decide: new port in ports.go vs. concrete type in `cmd/wad`.
- SC-001 (end-to-end in 2 minutes) includes manual QR scanning time — realistic for the manual test only.
- Deliberate design decision: the integration test uses a fake whatsmeow client rather than a real burner phone, keeping CI green without manual intervention.
