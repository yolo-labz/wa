# Implementation Plan: Domain Types and the Seven Port Interfaces

**Branch**: `002-domain-and-ports` | **Date**: 2026-04-06 | **Spec**: [`spec.md`](./spec.md)
**Input**: Feature specification from `/specs/002-domain-and-ports/spec.md`

## Summary

Build the hexagonal foundation: a pure-Go domain package (`internal/domain`), the seven port interfaces (`internal/app/ports.go`), an in-memory adapter that implements all seven ports under `internal/adapters/secondary/memory/`, and a shared contract test suite under `internal/app/porttest/` that any adapter can run against itself. After this feature lands, feature 003 (the whatsmeow secondary adapter) can begin by implementing the seven interfaces and pointing the contract suite at itself, with zero modifications to anything under `internal/domain` or `internal/app/ports.go`.

The technical approach is locked in [`research.md`](./research.md): sealed-interface variant pattern for `Message`, Watermill-style `RunContractSuite(t, factory)` for the test suite, `ctx context.Context` first parameter on every port method except `Allowlist.Allows`, hand-rolled JID parser to avoid importing whatsmeow.

## Technical Context

**Language/Version**: Go 1.22 minimum at the toolchain (per the constitution and `.golangci.yml run.go: "1.22"`); the development host is currently 1.26.1 in `go.mod`.
**Primary Dependencies**: Standard library only. No third-party Go modules added in this feature. The `go.sum` after `go mod tidy` should remain effectively empty (no transitive deps beyond what `go.mod` already declares, which is nothing).
**Storage**: None. Domain types are values; the in-memory adapter holds state in stdlib data structures (`map`, `slice`) protected by `sync.Mutex` where parallel-safe semantics are needed.
**Testing**: `go test ./...` with table tests, `t.Run` subtests, and the `internal/app/porttest/` shared suite. Race detector (`-race`) on every CI run. No mocking framework — in-memory adapter is the test double.
**Target Platform**: macOS arm64 + Linux (amd64/arm64) — the same matrix the future binaries target. This feature builds on every platform because it has zero non-stdlib dependencies.
**Project Type**: Single Go module (`github.com/yolo-labz/wa`), library-only at this layer. Two binaries (`cmd/wa`, `cmd/wad`) consume this code starting in feature 004; this feature adds nothing under `cmd/`.
**Performance Goals**: `go test ./...` MUST complete in under 5 seconds on a fresh clone (spec SC-002). Domain construction (parsing a JID, validating a Message) MUST complete in under 1 µs per call. The contract test suite MUST complete in under 1 second when run against the in-memory adapter.
**Constraints**: Zero non-stdlib imports in `internal/domain`; zero non-stdlib imports in `internal/app` except via the seven port interfaces (which are themselves stdlib-only signatures). No goroutines outside test helpers. No file I/O. No network. No `time.Now()` calls outside an injectable clock interface. No `context.TODO()` in non-test code.
**Scale/Scope**: ~600 lines of Go across ~12 files. ~30 test functions in `porttest/`. One maintainer, one workstation. The deliverable is library code that downstream features depend on, not user-visible behavior.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

The constitution at [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md) v1.0.0 has seven core principles. Each is evaluated below.

| # | Principle | Verdict | Evidence |
|---|---|---|---|
| I | Hexagonal core, library at arm's length (NON-NEGOTIABLE) | **PASS** | This feature *builds* the hexagonal core. FR-009 binds whatsmeow exclusion to the existing `core-no-whatsmeow` `depguard` rule. The contract test suite verifies the boundary mechanically. |
| II | Daemon owns state, CLI is dumb | **N/A** | No daemon code in this feature. The in-memory adapter is library state, not daemon state; the daemon composition root lands in feature 004. |
| III | Safety first, no `--force` ever (NON-NEGOTIABLE) | **PARTIAL — JUSTIFIED** | The `Allowlist` and `Action` domain types land here (the data structures). The rate limiter middleware, warmup ramp, audit log writer, and `<channel source="wa">` tag wrapper land in feature 004 because they all need the daemon's clock, persistence, and IPC. This split is the correct hexagonal layering: data vs orchestration. The constitution principle is satisfied because *every component the spec mandates is on disk before the first `Send` call leaves `wad`* — and `wad` does not exist yet. |
| IV | CGO is forbidden | **PASS** | Pure Go, stdlib only. No `import "C"` anywhere. |
| V | Spec-driven development with citations | **PASS** | `spec.md`, `plan.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md` all produced; every architectural choice cites a primary source. |
| VI | Tests use port-boundary fakes, not real WhatsApp | **PASS** | The in-memory adapter is User Story 3 and FR-010. The contract test suite is User Story 4 and FR-011. Together they are the load-bearing parts of this principle. |
| VII | Conventional commits, signed tags, no `--no-verify` | **PASS** | Every commit landing this feature uses `feat(...)` / `fix(...)` / `docs(...)` / `chore(...)`. The `lefthook` `commit-msg` hook configured in feature 001 enforces it locally. |

**Initial gate (Phase 0)**: PASS. One principle (III) is partially satisfied with explicit justification recorded above; the partial scope is enforced by the spec's FR-016 / FR-017 boundaries and is the correct hexagonal layering.

**Post-design gate (Phase 1)**: PASS. The Phase 1 artifacts (data-model, contracts, quickstart) describe the same library boundary the spec defines. They introduce no new architectural decisions; they only refine the Go signatures of decisions already locked.

**Complexity Tracking**: empty — no constitution violation requires justification beyond the principle III note above.

## Project Structure

### Documentation (this feature)

```text
specs/002-domain-and-ports/
├── spec.md              # /speckit:specify output                (committed)
├── research.md          # Phase 0: 5 tactical decisions cited    (this command)
├── plan.md              # this file                              (this command)
├── data-model.md        # Phase 1: Go types with field types     (this command)
├── quickstart.md        # Phase 1: 5-min verification runbook    (this command)
├── contracts/
│   ├── ports.md            # Go signatures + behavioral contract for the seven ports
│   └── domain.md           # Construction, validation, and method contract for the domain types
├── checklists/
│   └── requirements.md  # 37-item validation, 28 closed at plan time, 9 open until /implement
└── tasks.md             # Phase 2 output (/speckit:tasks)        (NOT created by /speckit:plan)
```

### Source Code (repository root)

```text
github.com/yolo-labz/wa/
├── internal/
│   ├── domain/
│   │   ├── jid.go              # JID value object + Parse + Validate + IsUser + IsGroup + String
│   │   ├── jid_test.go         # table tests for parser
│   │   ├── contact.go          # Contact struct + constructor
│   │   ├── group.go            # Group struct + Participants helpers
│   │   ├── message.go          # Message sealed interface + TextMessage + MediaMessage + ReactionMessage + Validate
│   │   ├── message_test.go     # variant validation tests
│   │   ├── session.go          # Session opaque handle + IsLoggedIn
│   │   ├── event.go            # Event sum type: MessageEvent, ReceiptEvent, ConnectionEvent, PairingEvent
│   │   ├── allowlist.go        # Allowlist struct + Allows(jid, action) + Add + Remove
│   │   ├── allowlist_test.go   # policy decision tests
│   │   ├── action.go           # Action enum: Read, Send, GroupAdd, GroupCreate
│   │   ├── ids.go              # MessageID, EventID type-aliased strings
│   │   ├── audit.go            # AuditEvent struct
│   │   └── errors.go           # ErrInvalidJID, ErrMessageTooLarge, ErrEmptyBody — typed errors
│   ├── app/
│   │   ├── ports.go            # the seven interfaces, full Go signatures, doc comments
│   │   └── porttest/
│   │       ├── suite.go        # RunContractSuite(t, factory) entrypoint
│   │       ├── sender.go       # MessageSender contract tests
│   │       ├── stream.go       # EventStream contract tests
│   │       ├── directory.go    # ContactDirectory contract tests
│   │       ├── groups.go       # GroupManager contract tests
│   │       ├── session.go      # SessionStore contract tests
│   │       ├── allowlist.go    # Allowlist contract tests (decision-table)
│   │       └── audit.go        # AuditLog contract tests
│   └── adapters/
│       └── secondary/
│           └── memory/
│               ├── adapter.go      # struct that satisfies all seven ports
│               ├── clock.go        # injectable Clock interface (Now() time.Time)
│               └── adapter_test.go # invokes RunContractSuite(t, NewForTest)
└── cmd/                    # untouched — .gitkeep placeholders survive
```

The eight `.gitkeep` files under `cmd/wa`, `cmd/wad`, `internal/adapters/primary/socket`, `internal/adapters/secondary/{whatsmeow,sqlitestore,slogaudit}` MUST survive untouched. The four directories that currently hold `.gitkeep`s and *will* receive Go source in this feature are `internal/domain`, `internal/app`, `internal/app/porttest` (new directory), and `internal/adapters/secondary/memory` — when the first real `.go` file lands in each, that file's commit deletes the corresponding `.gitkeep` in the same diff.

**Structure Decision**: hexagonal / ports-and-adapters per [`CLAUDE.md`](../../CLAUDE.md) §"Repository layout". The constitution Principle I locks the dependency direction (`domain → ∅`, `app → domain`, `adapters → app + domain`); this feature is the first time those packages contain real code, so this plan is also the first time the dependency direction can be enforced by `golangci-lint` against actual imports.

## Phase 0 — Research

**Status**: complete. See [`research.md`](./research.md).

The five tactical decisions resolved in Phase 0:

1. **D1** Sum-typed `Message` via sealed-interface variant pattern (Go community idiom, used by `go-cmp` and `golang.org/x/tools/go/ast/inspector`)
2. **D2** `RunContractSuite(t, factory)` shared test suite (Watermill `pubsub.TestPubSub` pattern)
3. **D3** `ctx context.Context` first parameter on every port method except `Allowlist.Allows`
4. **D4** Hand-rolled `JID` parser in `internal/domain/jid.go`, no third-party phone library, ITU-T E.164 length range 8–15
5. **D5** Allowlist policy decision lives in domain (pure function); rate limiter middleware lives in `app` layer (feature 004)

Zero `[NEEDS CLARIFICATION]` markers remain. Phase 0 produced no contradictions with `CLAUDE.md` or the constitution.

## Phase 1 — Design & Contracts

**Status**: complete in this command. Three artifacts land alongside this `plan.md`:

- **[`data-model.md`](./data-model.md)** — every domain type with Go field types, methods, validation rules, lifecycle states, and invariants. This is the Go-level equivalent of UML class diagrams; the implementation in feature 002's `/speckit:implement` is a literal transcription.
- **[`contracts/ports.md`](./contracts/ports.md)** — the seven port interfaces with full Go signatures, doc comments, and a behavioural contract per method (preconditions, postconditions, error categories, idempotency). This is what `internal/app/ports.go` will literally contain (modulo language-level details like import block).
- **[`contracts/domain.md`](./contracts/domain.md)** — the construction, validation, and method contracts for every domain type. Mirrors `contracts/ports.md` but for `internal/domain/*`.
- **[`quickstart.md`](./quickstart.md)** — the executable form of the deliverables checklist (CHK029–CHK037). A fresh contributor runs the steps and gets pass/fail in under 5 minutes.

**Agent context update** (`update-agent-context.sh`): **skipped intentionally**, same rationale as feature 001. `CLAUDE.md` is hand-authored, lacks the `<!-- MANUAL ADDITIONS START/END -->` markers the script preserves, and is the architectural source of truth referenced from research.md, README.md, SECURITY.md, and the spec. Auto-overwriting it would do net negative work. The right time to introduce the auto-update remains "if and when speckit auto-updates start delivering value" — not yet.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

There are no constitution violations to justify. The Principle III "PARTIAL" verdict above is the correct hexagonal layering, not a complexity smell — the data structures (`Allowlist`, `Action`) land in this feature; the orchestration (rate limiter, warmup, audit log writer) lands in feature 004 because it depends on the daemon's clock and IPC. The complexity table is intentionally empty.

## Followup wiring for `/speckit:tasks`

When `/speckit:tasks` runs against this plan, the resulting `tasks.md` should produce a real implementation plan (unlike feature 001's retrospective). Expected shape:

| Phase | Tasks | Notes |
|---|---|---|
| Setup (T001..T002) | Create `internal/app/porttest/` directory; ensure `.gitkeep` deletions are scoped | No new dependencies |
| Foundational (T003..T006) | Write `internal/domain/errors.go`, `internal/domain/action.go`, `internal/domain/ids.go`, `internal/domain/jid.go` | The dependency root of every other type |
| US1 Domain types (T007..T014) | One task per remaining domain type with its `_test.go` | Many `[P]` because each file is independent |
| US2 Ports (T015..T016) | Write `internal/app/ports.go`, document the seven interfaces | Single file, sequential |
| US3 In-memory adapter (T017..T020) | Write `internal/adapters/secondary/memory/adapter.go`, `clock.go`, the test that invokes the suite | Needs ports + domain to compile |
| US4 Contract test suite (T021..T028) | One task per port (`suite.go` + per-port test file) | Many `[P]` |
| Polish (T029..T035) | `golangci-lint run`, `go test -race`, `go vet`, doc comment audit, deliberate-violation depguard test, commit + push | Includes the SC-004 manual depguard violation test |

Roughly 35 tasks. ~600 lines of Go. Single-session implementation plausible.

## Artifacts produced by this plan

- [`plan.md`](./plan.md) — this file
- [`research.md`](./research.md) — Phase 0 (already landed in this command)
- [`data-model.md`](./data-model.md) — Phase 1 entity definitions
- [`contracts/ports.md`](./contracts/ports.md)
- [`contracts/domain.md`](./contracts/domain.md)
- [`quickstart.md`](./quickstart.md) — five-minute verification runbook
