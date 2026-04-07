---
description: "Task list for feature 002-domain-and-ports"
---

# Tasks: Domain Types and the Seven Port Interfaces

**Input**: Design documents from `/specs/002-domain-and-ports/`
**Prerequisites**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`contracts/ports.md`](./contracts/ports.md), [`contracts/domain.md`](./contracts/domain.md), [`quickstart.md`](./quickstart.md)

**Tests**: Required (Constitution Principle VI). Tests are not optional in this project.

**Organization**: Tasks are grouped by user story from spec.md (US1 = domain types; US2 = port interfaces; US3 = in-memory adapter; US4 = contract test suite). All tasks include the absolute file path they produce.

## Format: `[ ] [TaskID] [P?] [Story?] Description with absolute file path`

- `[x]` = completed
- `[P]` = parallel-eligible (different files, no dependency on incomplete tasks)
- `[USn]` = applies only to user-story phases; setup/foundational/polish phases carry no story label

---

## Phase 1: Setup

**Purpose**: Pre-implementation housekeeping before any source file is written.

- [ ] T001 Verify the deliberate-violation depguard test still works against the empty `internal/domain/` directory by `cd /Users/notroot/Documents/Code/WhatsAppAutomation && bash -c 'echo "package domain
import _ \"go.mau.fi/whatsmeow\"" > internal/domain/violation.go && golangci-lint run ./internal/domain/... 2>&1 | grep core-no-whatsmeow; rm internal/domain/violation.go'`
- [ ] T002 Delete the placeholder `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/.gitkeep` (in the same commit as the first real `.go` file under that path)
- [ ] T003 Delete the placeholder `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/.gitkeep` (in the same commit as `internal/app/ports.go`)
- [ ] T004 Delete the placeholder `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/memory/.gitkeep` (in the same commit as `adapter.go`)
- [ ] T005 Create directory `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/` (no .gitkeep needed; immediately populated by T021)

---

## Phase 2: Foundational

**Purpose**: Files every other domain type depends on. MUST land before US1 begins.

- [ ] T006 Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/errors.go` with the six sentinel errors per `contracts/domain.md §errors.go`
- [ ] T007 [P] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/ids.go` with `MessageID` and `EventID` named types per `contracts/domain.md §ids.go`
- [ ] T008 [P] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/action.go` with the `Action` enum (`iota+1` start) per `contracts/domain.md §action.go`
- [ ] T009 Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/jid.go` with `JID` value object, `Parse`, `ParsePhone`, `MustJID`, and helpers per `contracts/domain.md §jid.go`

---

## Phase 3: User Story 1 — Domain types (Priority: P1)

**Goal**: Pure-Go domain package with all 11 types and their invariants.

**Independent test**: Open a Go REPL, import `internal/domain`, construct a `JID` from `"+5511999999999"`, build a `TextMessage`, call `Validate()`, get a usable value with no whatsmeow imports anywhere in the dependency tree (`go list -deps ./internal/domain/...`).

- [ ] T010 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/contact.go` with `Contact` struct + `NewContact` + `DisplayName` + `IsZero` per `contracts/domain.md §contact.go`
- [ ] T011 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/group.go` with `Group` struct + `NewGroup` + `HasParticipant` + `IsAdmin` + `Size` per `contracts/domain.md §group.go`
- [ ] T012 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/message.go` with `Message` sealed interface, `MaxTextBytes`, `MaxMediaBytes`, and the three variants `TextMessage`, `MediaMessage`, `ReactionMessage` with `Validate` methods per `contracts/domain.md §message.go`
- [ ] T013 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/event.go` with `Event` sealed interface, four variants (`MessageEvent`, `ReceiptEvent`, `ConnectionEvent`, `PairingEvent`) and the three supporting enums (`ReceiptStatus`, `ConnectionState`, `PairingState`) per `contracts/domain.md §event.go`
- [ ] T014 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/session.go` with `Session` struct + `NewSession` + `IsLoggedIn` per `contracts/domain.md §session.go`
- [ ] T015 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/allowlist.go` with `*Allowlist` (pointer, mutex-protected per the asymmetry note in `data-model.md`) + `NewAllowlist` + `Allows` + `Grant` + `Revoke` + `Entries` + `Size` per `contracts/domain.md §allowlist.go`
- [ ] T016 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/audit.go` with `AuditEvent` struct + `AuditAction` enum (with `Audit` prefix per the defensive convention in `data-model.md`) + `NewAuditEvent` + `String()` per `contracts/domain.md §audit.go`
- [ ] T017 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/jid_test.go` with the ~25 table-driven test cases enumerated in `contracts/domain.md §"Test counts"`
- [ ] T018 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/action_test.go` (~5 cases), `contact_test.go` (~4), `group_test.go` (~6), `message_test.go` (~12), `event_test.go` (~8), `session_test.go` (~4), `allowlist_test.go` (~8 including parallel race tests), `audit_test.go` (~4), each per the enumerated invariants in `contracts/domain.md`
- [ ] T019 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/ids_compile_test.go` with `//go:build never` carrying the four cross-type-assignment cases per `contracts/domain.md §ids.go` (CHK023 — separate target verifies the compile error)
- [ ] T020 [US1] Add a Make target or shell helper that runs `go build -tags never ./internal/domain/` and asserts the build fails with the four expected cross-type assignment errors

**Checkpoint**: US1 testable once T010-T020 land. `go test ./internal/domain/...` produces ~76 passing test functions; `go list -deps ./internal/domain/...` returns only stdlib packages.

---

## Phase 4: User Story 2 — Port interfaces (Priority: P1)

**Goal**: `internal/app/ports.go` declares the seven port interfaces with full Go signatures, doc comments, and zero non-stdlib imports beyond `context`, `time`, and `internal/domain`.

**Independent test**: `go vet ./internal/app/...` exits 0; `golangci-lint run ./internal/app/...` exits 0 (the `core-no-whatsmeow` `depguard` rule fires zero times).

- [ ] T021 [US2] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/ports.go` with the seven interface declarations from `contracts/ports.md` §1-§7, including doc comments naming the contract clauses each implementation must satisfy
- [ ] T022 [US2] Verify `golangci-lint run ./internal/app/...` exits 0 with the `core-no-whatsmeow` rule active

**Checkpoint**: US2 testable once T021-T022 land. The seven interfaces compile against the domain types from US1.

---

## Phase 5: User Story 3 — In-memory adapter (Priority: P2)

**Goal**: `internal/adapters/secondary/memory/adapter.go` implements all seven port interfaces deterministically with no goroutines, no network, no time except via injectable clock.

**Independent test**: `go test -race ./internal/adapters/secondary/memory/...` passes with the contract suite invocation in `adapter_test.go`.

- [ ] T023 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/memory/clock.go` with the package-scoped `Clock` interface and a `RealClock` and `FakeClock` per the "Why Clock is not a port" discussion in `data-model.md`
- [ ] T024 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/memory/adapter.go` implementing all seven port interfaces from `contracts/ports.md`. The struct holds: `[]domain.MessageEvent` for the event log, `map[domain.JID]domain.Contact` for contacts, `map[domain.JID]domain.Group` for groups, `*domain.Allowlist` injected from outside, `[]domain.AuditEvent` for the audit log, an `EventStream` channel with bounded buffer, and a `Clock`
- [ ] T025 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/memory/adapter_test.go` invoking `porttest.RunContractSuite(t, NewForTest)` so the in-memory adapter is validated against the contract suite from US4

**Checkpoint**: US3 testable once T023-T025 land. The in-memory adapter passes the contract suite (which lands in US4).

---

## Phase 6: User Story 4 — Contract test suite (Priority: P2)

**Goal**: `internal/app/porttest/` contains a shared contract test suite that any adapter implementing the seven ports can run against itself, with the failure-mode reporting format from `contracts/ports.md §"Failure-mode reporting"`.

**Independent test**: an external consumer test (not in `porttest/`) invokes `porttest.RunContractSuite(t, factory)` and the suite executes against the in-memory adapter from US3.

- [ ] T026 [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/suite.go` with the `Adapter` interface (intersection of the seven ports) and the `RunContractSuite(t *testing.T, factory func(*testing.T) Adapter)` entrypoint
- [ ] T027 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/sender.go` with the six MS1-MS6 contract clauses from `contracts/ports.md §1`
- [ ] T028 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/stream.go` with the six ES1-ES6 contract clauses from `contracts/ports.md §2`
- [ ] T029 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/directory.go` with the four ContactDirectory cases per `contracts/ports.md §3`
- [ ] T030 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/groups.go` with the four GroupManager cases per `contracts/ports.md §4`
- [ ] T031 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/session.go` with the six SessionStore cases per `contracts/ports.md §5`
- [ ] T032 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/allowlist.go` with the six AL1-AL6 cases per `contracts/ports.md §6`
- [ ] T033 [P] [US4] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/audit.go` with the five AuditLog cases per `contracts/ports.md §7`

**Checkpoint**: US4 testable once T026-T033 land. The full contract suite executes; the in-memory adapter from US3 passes; a fresh consumer test outside `porttest/` can invoke it.

---

## Phase 7: Polish & cross-cutting

- [ ] T034 Run `go test -race -count=1 ./...` on the whole repository; record the wall time and update SC-002 in `specs/002-domain-and-ports/spec.md` with the measured value if it differs from "under 5 seconds"
- [ ] T035 Run `golangci-lint run ./...` with the v2.x linter; assert zero findings
- [ ] T036 Run `go vet ./...`; assert zero findings (CHK037 in `requirements.md`)
- [ ] T037 Run the deliberate-violation depguard test from `quickstart.md §3`; assert the rule fires
- [ ] T038 Tick CHK029-CHK037 in `specs/002-domain-and-ports/checklists/requirements.md` after the corresponding deliverable lands
- [ ] T039 Run `bash specs/002-domain-and-ports/quickstart.md` end-to-end (or follow it manually); assert every block exits 0
- [ ] T040 Commit each phase as a single conventional-commit (`feat(domain):`, `feat(ports):`, `feat(adapter):`, `feat(porttest):`, `chore(test):`) and push to `origin/002-domain-and-ports`
- [ ] T041 Tag `v0.0.2-domain-and-ports` (annotated) marking the end of feature 002

---

## Dependencies

```text
T001..T005 (Setup) ──▶ T006..T009 (Foundational) ──┐
                                                    ├──▶ T010..T020 (US1 Domain types)
                                                    │           │
                                                    │           ↓
                                                    └──▶ T021..T022 (US2 Ports) ──┐
                                                                                    │
                                                                                    ├──▶ T023..T025 (US3 In-memory)
                                                                                    │              ↑
                                                                                    └──▶ T026..T033 (US4 Contract suite)
                                                                                                    │
                                                                                                    ↓
                                                                                       T034..T041 (Polish)
```

- T006 (`errors.go`) blocks every other domain file because they all import sentinel errors
- T009 (`jid.go`) blocks T010 (`contact.go`) and T011 (`group.go`) because both embed JID
- T021 (`ports.go`) blocks both T024 (the in-memory adapter implements them) and T026 (the suite asserts on them)
- T023 (`clock.go`) blocks T024 (the adapter takes a Clock)
- T026 (`suite.go`) blocks T025 (the adapter test invokes the suite)
- T034..T039 (Polish) block T040 (commit) which blocks T041 (tag)
- T020 is the verification step for T019 (the build-tag compile error test)

## Parallel example

Inside Phase 2 (Foundational), T007 and T008 are `[P]` because `ids.go` and `action.go` are independent of each other.

Inside Phase 3 (US1), T010, T011, T013, T014, T016 are all `[P]` because they touch disjoint files. T012 (`message.go`) and T015 (`allowlist.go`) are sequential against the rest because they have richer dependencies. T017 and T018 (the test files) are `[P]` against everything because they live next to their source files and don't change package state.

Inside Phase 6 (US4), T027-T033 are all `[P]` because each per-port test file is independent.

## Implementation strategy

**MVP scope** for feature 002 = User Stories 1, 2, 3, 4 (Phases 3-6). Setup and Foundational are blocking prerequisites; Polish is closure.

The natural commit boundaries are:

| Commit | Phase coverage |
|---|---|
| `feat(domain): add errors, ids, action, jid` | T006-T009 |
| `feat(domain): add contact, group, message, event, session, allowlist, audit + tests` | T010-T018 |
| `chore(domain): add cross-type assignment compile-error gate` | T019-T020 |
| `feat(ports): declare the seven port interfaces in internal/app/ports.go` | T021-T022 |
| `feat(porttest): contract suite with seven per-port files` | T026-T033 |
| `feat(adapter/memory): in-memory adapter passing the contract suite` | T023-T025 |
| `chore(test): polish — go test, lint, vet, depguard, quickstart` | T034-T039 |
| `chore(release): tag v0.0.2-domain-and-ports` | T040-T041 |

8 commits total. The order respects the dependency graph: ports must exist before either the adapter or the suite can compile against them. The suite should land before the adapter so the adapter's test can immediately invoke it.

## Out of scope (deferred to feature 003+)

- Implementing any port against `go.mau.fi/whatsmeow` (feature 003)
- Wiring `wad`'s composition root, daemon process, or unix-socket server (feature 004)
- Pairing a real WhatsApp number, sending or receiving any message (feature 003)
- Building the `wa-assistant` Claude Code plugin in its separate repo (feature 007)
- Notarising or releasing any binary (feature 006)
