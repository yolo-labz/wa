# Spec Quality Checklist: Socket Primary Adapter

**Purpose**: Validate the quality, testability, and completeness of the requirements captured in `spec.md` for feature 004.
**Created**: 2026-04-08
**Feature**: [spec.md](../spec.md)

## Content Quality

- [X] CHK001 Spec is written for stakeholders, not implementers: avoids Go package names, library names, or file paths in functional requirements.
- [X] CHK002 Every FR is a single testable statement (one MUST / SHOULD per bullet).
- [X] CHK003 Spec identifies the scope boundary between this feature and adjacent features (002, 003, 005, 006) explicitly in an "Out of Scope" section.
- [X] CHK004 Spec explains the "why" behind the P1 items, not just the "what".
- [X] CHK005 No vague qualifiers ("fast", "scalable", "robust") without a measurable success criterion attached.

## Requirement Completeness

- [X] CHK006 All five user stories have explicit acceptance scenarios in Given/When/Then form.
- [X] CHK007 Each user story has an "Independent Test" description that does not depend on any other story being implemented.
- [X] CHK008 Edge cases list covers: symlink attacks, oversized messages, partial frames, dispatcher panics, stale sockets, buffer overflow, multiple subscriptions per connection, `sun_path` length limit.
- [X] CHK009 Error-code mapping is called out as a mandatory deliverable (FR-011, FR-039), not left as "TBD".
- [X] CHK010 Graceful shutdown semantics are specified: drain, deadline, per-request cancellation, subscription notification, socket unlink, lock release.
- [X] CHK011 Peer-credential check is specified independently for Linux (`SO_PEERCRED`) and macOS (`LOCAL_PEERCRED` / `getpeereid`).
- [X] CHK012 Dispatcher interface is named and its contract is scoped (input shape, return shape, panic recovery, event source semantics).
- [X] CHK013 Observability requirements enumerate exactly which events are logged and which data is forbidden from logs (FR-036, FR-038).

## Requirement Clarity

- [X] CHK014 "Same-user-only" auth is defined precisely: peer uid equality, not group membership, not filesystem ACLs.
- [X] CHK015 The OS-specific socket path is given exact filesystem paths, not a reference to an RFC.
- [X] CHK016 The framing rule is unambiguous: one JSON object per line, single `\n`, no embedded newlines, 1 MiB cap.
- [X] CHK017 The JSON-RPC error code for every reserved server-side condition is listed or scheduled to be listed in the error-code table.
- [X] CHK018 The phrase "graceful shutdown" has a quantified deadline (5 seconds default) and a hard timeout (FR-032).
- [X] CHK019 The backpressure trigger is quantified (buffer size ≥ 1024, closed within 1 second of fill).
- [X] CHK020 Acceptance scenarios for US3 (streaming) include both the happy path and the backpressure path.

## Requirement Consistency

- [X] CHK021 FR-003 (mode 0600) is consistent with the existing storage permissions convention used by feature 003's `sqlitestore`/`sqlitehistory` adapters.
- [X] CHK022 The file-lock mechanism chosen here (rogpeppe/go-internal/lockedfile) is consistent with feature 003's choice; no new locking library is introduced.
- [X] CHK023 The depguard rule from feature 003 (core must not import whatsmeow) is explicitly extended to this feature in FR / SC-010.
- [X] CHK024 The error-code range (`-32099..-32000`) is consistent with the JSON-RPC 2.0 specification and does not overlap protocol-reserved codes.
- [X] CHK025 The scope of "transport layer" is consistent across the Overview, User Stories, and Out of Scope sections: no FR talks about `send` / `pair` / `status` semantics.

## Measurability

- [X] CHK026 Every success criterion has a numeric target or a verifiable binary condition.
- [X] CHK027 SC-001 (<10 ms roundtrip) is verifiable with a benchmark.
- [X] CHK028 SC-002 (peer-cred reject <50 ms) is verifiable with a timing assertion in a test.
- [X] CHK029 SC-003 (already-running <500 ms) is verifiable with a second in-process server instance.
- [X] CHK030 SC-008 (`go test -race`, zero leaked goroutines) is verifiable by running the suite.
- [X] CHK031 SC-009 (<10 s total contract-suite wall time) is verifiable with a Makefile target or `go test -v`.

## Scope Coverage

- [X] CHK032 Every FR is mapped to at least one User Story by topic (transport → US1, auth → US2, streaming → US3, single-instance → US4, lifecycle → US5).
- [X] CHK033 Every User Story is scoped narrowly enough to be shippable without any of the others being implemented.
- [X] CHK034 No FR depends on a method that will only exist in feature 005 (`send`, `pair`, etc.); all FRs can be tested with a fake dispatcher.
- [X] CHK035 The deliverable contract files (`contracts/wire-protocol.md`, `contracts/dispatcher.md`) are called out in FR-039/FR-041.

## Ambiguities & Conflicts

- [X] CHK036 No `[NEEDS CLARIFICATION]` markers remain in the spec.
- [X] CHK037 No two FRs contradict each other (e.g., no "close on error" vs. "keep connection open" conflict for the same condition).
- [X] CHK038 The definition of "graceful shutdown" is consistent between US5 and FR-031..FR-035.
- [X] CHK039 Batch JSON-RPC is explicitly deferred (Edge Cases + Out of Scope), not half-specified.

## Notes

- All items above pass on first draft.
- The scope-split decision (socket transport here, use cases in feature 005) is documented at the top of the Overview and enforced throughout the FRs.
- If reviewers disagree with the scope split, the feature can be resized by merging with feature 005 — no rewrite required, since the Dispatcher seam is explicit.
- The three potential clarification areas I considered but resolved with reasonable defaults:
  - **Backpressure policy**: close connection (not drop events, not unbounded queue). Documented in FR-024.
  - **Concurrency per connection**: bounded pipeline (up to 32 in-flight). Documented in FR-028.
  - **Batch JSON-RPC**: deferred, explicit in Out of Scope + Edge Cases.
