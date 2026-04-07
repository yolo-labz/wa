# Requirements Checklist: Domain Types and the Seven Port Interfaces

**Purpose**: Validate that `specs/002-domain-and-ports/spec.md` meets speckit content-quality, requirement-completeness, and feature-readiness criteria, and that the resulting code lands inside the constitution's principles I, II, V, and VI.
**Created**: 2026-04-06
**Feature**: [`spec.md`](../spec.md)

## Content quality

- [x] CHK001 Spec is written for an external maintainer, not for this session: no Claude-Code-only references, no swarm output paste-ins, no agent IDs
- [x] CHK002 Every section in `.specify/templates/spec-template.md` that applies to this feature is filled out; sections that do not apply have been removed entirely
- [x] CHK003 No more than 3 `[NEEDS CLARIFICATION: ...]` markers remain in the spec (current: 0)
- [x] CHK004 The "Input" line at the top of the spec quotes the user's actual `/speckit:specify` arguments
- [x] CHK005 The "Status" line is set to `Draft`
- [x] CHK006 No embedded checklists inside the spec body — checklists live only in this file

## Requirement completeness

- [x] CHK007 Every functional requirement is testable: a developer can write a Go test that asserts FR-NNN as pass/fail without inventing assumptions
- [x] CHK008 Every functional requirement maps to at least one user story or edge case
- [x] CHK009 Every user story has an "Independent Test" sentence describing how to verify it without the other stories
- [x] CHK010 Every user story has at least one Given/When/Then acceptance scenario
- [x] CHK011 Every Key Entity referenced in the requirements appears in the Key Entities section with a definition
- [x] CHK012 Every assumption is recorded under Assumptions; nothing load-bearing is implicit
- [x] CHK013 Edge cases enumerate at least five failure or boundary conditions (current: 6)
- [x] CHK014 The spec covers all four user stories (domain, ports, in-memory adapter, contract suite); none of the four can be silently dropped during implementation

## Constitution alignment

- [x] CHK015 Spec respects Principle I (hexagonal core): FR-009 forbids whatsmeow imports under `internal/{domain,app}` and ties enforcement to the existing depguard rule
- [x] CHK016 Spec respects Principle II (daemon owns state): FR-016 explicitly excludes daemon, CLI, socket, SQLite, and live WhatsApp from this feature's scope
- [x] CHK017 Spec respects Principle V (spec-driven with citations): every architectural claim points back to CLAUDE.md or the constitution; no naked "best practice" claims
- [x] CHK018 Spec respects Principle VI (port-boundary fakes): User Story 3 mandates the in-memory adapter as a deliverable, not as a future extension
- [x] CHK019 Spec respects Principle VII (conventional commits): the commit landing this spec uses the `feat(spec):` prefix
- [x] CHK020 Spec does not relitigate any constitution principle without amending the constitution in the same PR

## Success criteria quality

- [x] CHK021 Each SC-NNN is measurable (cites a count, a duration, an exit code, or a yes/no observable)
- [x] CHK022 Each SC-NNN is technology-agnostic at the user-visible level (it may name `golangci-lint` and `go test` because those are the project's binding tooling per the constitution, but it does not reference whatsmeow internals)
- [x] CHK023 Each SC-NNN is verifiable from outside the implementation (a third party can run the check)
- [x] CHK024 Success criteria collectively cover the four user stories and the linter gate

## Feature readiness

- [x] CHK025 User stories are prioritized P1/P1/P2/P2 reflecting actual blocking relationships (US1 and US2 are co-equal foundations; US3 and US4 are downstream)
- [x] CHK026 The spec lists what is in scope and what is explicitly out of scope (FR-016, FR-017)
- [x] CHK027 The spec does not assume any work that has not landed yet, except via clearly named future features (003 whatsmeow, 004 daemon, 005 CLI, 006 distribution)
- [x] CHK028 The spec accounts for the failure mode where a port turns out to be impossible to implement against whatsmeow (Edge Case: amend spec + constitution + suite together)

## Deliverables present (re-run after `/speckit:plan` and `/speckit:implement`)

- [x] CHK029 `internal/domain/` contains source files for `JID`, `Contact`, `Group`, `Message`, `Session`, `Event`, `Allowlist`, `Action`, `MessageID`, `EventID`, `AuditEvent` — each with at least one method beyond accessors
- [x] CHK030 `internal/app/ports.go` declares exactly the seven interfaces with the names from CLAUDE.md §"Ports" and the signatures from this spec's FR-007 / FR-008
- [x] CHK031 `internal/adapters/secondary/memory/` implements all seven ports deterministically with no goroutines and no network calls
- [x] CHK032 `internal/app/porttest/` contains a contract test suite that any adapter can run; the in-memory adapter passes it
- [x] CHK033 `golangci-lint run ./...` exits 0 on this branch with the existing `.golangci.yml` config (depguard `core-no-whatsmeow` rule active)
- [x] CHK034 `go test ./...` exits 0 in under 5 seconds on a fresh clone with no env vars set
- [x] CHK035 `go vet ./...` exits 0 with zero comment-related findings
- [x] CHK036 No file under `cmd/` was modified by this feature (the `.gitkeep` placeholders survive untouched)
- [x] CHK037 The branch `002-domain-and-ports` is pushed to `origin` and `git status` is clean at feature close

## Notes

- Items CHK001–CHK028 validate the spec itself and were checked during spec authoring. They will be re-validated by `/speckit:plan` and `/speckit:tasks` before implementation begins.
- Items CHK029–CHK037 validate the deliverables and must remain unchecked until `/speckit:implement` finishes the work. They are the executable definition of "feature 002 done."
- If any item under "Deliverables present" cannot be checked at feature close, the reason must be documented in this file's Notes section, never silently left blank.
