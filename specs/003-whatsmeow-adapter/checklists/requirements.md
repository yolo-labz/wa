# Requirements Checklist: whatsmeow Secondary Adapter

**Purpose**: Validate that `specs/003-whatsmeow-adapter/spec.md` meets speckit content-quality, requirement-completeness, and feature-readiness criteria, and that the resulting code preserves the hexagonal invariant from feature 002.
**Created**: 2026-04-07
**Feature**: [`spec.md`](../spec.md)

## Content quality

- [x] CHK001 Spec is written for an external maintainer, not for this session: no agent IDs, no swarm pasted output
- [x] CHK002 Every section in `.specify/templates/spec-template.md` that applies to this feature is filled out; sections that do not apply are removed entirely
- [x] CHK003 No more than 3 `[NEEDS CLARIFICATION: ...]` markers remain in the spec (current: 3 — FR-018, FR-019, FR-020)
- [x] CHK004 The "Input" line at the top of the spec quotes the user's actual `/speckit:specify` arguments
- [x] CHK005 The "Status" line is set to `Draft`
- [x] CHK006 No embedded checklists inside the spec body — checklists live only in this file

## Requirement completeness

- [x] CHK007 Every functional requirement is testable: a developer can write a Go test that asserts FR-NNN as pass/fail without inventing assumptions (excluding the 3 NEEDS CLARIFICATION items, which are explicit deferrals)
- [x] CHK008 Every functional requirement maps to at least one user story or edge case
- [x] CHK009 Every user story has an "Independent Test" sentence describing how to verify it without the other stories
- [x] CHK010 Every user story has at least one Given/When/Then acceptance scenario
- [x] CHK011 Every Key Entity referenced in the requirements appears in the Key Entities section with a definition
- [x] CHK012 Every assumption is recorded under Assumptions; nothing load-bearing is implicit
- [x] CHK013 Edge cases enumerate at least five failure or boundary conditions (current: 6)
- [x] CHK014 The spec covers all four user stories (use-case parity, pairing, single-instance store, Renovate loop); none of the four can be silently dropped during implementation

## Constitution alignment

- [x] CHK015 Spec respects Principle I (hexagonal core): FR-002, FR-017, SC-002, SC-003 enforce; the depguard rule is named in spec, plan, and constitution
- [x] CHK016 Spec respects Principle II (daemon owns state): FR-016 explicitly excludes daemon, CLI, socket, rate limiter from this feature's scope
- [x] CHK017 Spec respects Principle III (safety first, partial): the audit log port is implemented as an in-memory ring buffer here; the file-backed writer lands in feature 004 alongside the rate limiter middleware
- [x] CHK018 Spec respects Principle IV (no CGO): FR-005 + Assumptions name `modernc.org/sqlite` as the driver
- [x] CHK019 Spec respects Principle V (spec-driven with citations): every architectural choice cites CLAUDE.md, the constitution, or feature 002's research dossier
- [x] CHK020 Spec respects Principle VI (port-boundary fakes): the contract suite from feature 002 is the validation gate (FR-013), not bespoke mocks
- [x] CHK021 Spec respects Principle VII (conventional commits): every commit landing this feature uses `feat(adapter):` / `feat(sqlitestore):` / `chore(test):` / `docs(spec):`

## Architecture quality (carry-over from feature 002 patterns)

- [x] CHK022 Spec names the depguard rule `core-no-whatsmeow` BY NAME, not by description (FR-002)
- [x] CHK023 Spec lists every whatsmeow `events.*` type the adapter must translate, exhaustively (FR-004)
- [x] CHK024 Spec lists the 8 whatsmeow client setup flags from `mautrix/whatsapp/pkg/connector/client.go` BY NAME (FR-009)
- [x] CHK025 Spec ties the `clientCtx` lifetime rule to the prior research dossier finding (FR-012, references the aldinokemal lesson from feature 002 research §D3)
- [x] CHK026 Spec defines the failure mode for `events.LoggedOut` precisely (FR-010)
- [x] CHK027 Spec specifies the QR-in-terminal rendering library (`mdp/qrterminal/v3 GenerateHalfBlock`) by name (FR-008)
- [x] CHK028 Spec names the file lock primitive (`flock(LOCK_EX|LOCK_NB)`) and the failure mode of NOT locking (corrupted ratchet store) (FR-007, US3)

## Success criteria quality

- [x] CHK029 Each SC-NNN is measurable (cites a count, a duration, an exit code, or a yes/no observable)
- [x] CHK030 Each SC-NNN is verifiable from outside the implementation (a third party with a paired number can run the check)
- [x] CHK031 SC-002 and SC-003 explicitly assert that feature 003 introduces zero changes to `internal/domain/` or `internal/app/ports.go` — the architectural test of US1
- [x] CHK032 SC-008 caps total adapter LOC at 1500 to prevent the adapter from accreting business logic that should live elsewhere

## Feature readiness

- [x] CHK033 User stories are prioritized P1/P1/P1/P2 reflecting actual blocking relationships (US1, US2, US3 are all required for any usable adapter; US4 is operational backstop)
- [x] CHK034 The spec lists what is in scope and what is explicitly out of scope (FR-016, FR-017)
- [x] CHK035 The spec does not assume any work that has not landed yet, except via clearly named future features (004 daemon, 005 CLI, 006 distribution, 007 plugin)
- [x] CHK036 The 3 `[NEEDS CLARIFICATION]` markers are scoped to genuinely uncertain v0 design choices (queue-on-disconnect, history-sync volume, history-event surfacing) and will be resolved by `/speckit:clarify` before `/speckit:plan`

## Deliverables present (re-run after `/speckit:clarify`, `/speckit:plan`, `/speckit:tasks`, `/speckit:implement`)

- [ ] CHK037 `/speckit:clarify` has been run and the 3 NEEDS CLARIFICATION markers (FR-018, FR-019, FR-020) are resolved with concrete answers
- [ ] CHK038 `internal/adapters/secondary/whatsmeow/` contains the `Adapter` struct implementing all seven port interfaces from feature 002
- [ ] CHK039 `internal/adapters/secondary/sqlitestore/` contains the SQLite container constructor with `flock` enforcement
- [ ] CHK040 The contract test suite from `internal/app/porttest/` runs against the new adapter via `go test -tags integration` and passes (with a paired burner) OR is documented as deferred to the next session if no burner is available
- [ ] CHK041 `git diff main..003-whatsmeow-adapter -- internal/domain internal/app/ports.go` returns zero output
- [ ] CHK042 `golangci-lint run ./...` exits 0 with the existing `.golangci.yml` config (depguard `core-no-whatsmeow` rule active)
- [ ] CHK043 `go test ./...` exits 0 (unit tests for the JID translator, event translator, file-permission setter, and flock guard run unconditionally)
- [ ] CHK044 The `cmd/` `.gitkeep` placeholders are still untouched
- [ ] CHK045 The branch `003-whatsmeow-adapter` is pushed to `origin` and `git status` is clean at feature close

## Notes

- Items CHK001–CHK036 validate the spec itself and were checked during spec authoring.
- Items CHK037–CHK045 validate the deliverables and remain unchecked until `/speckit:clarify`, `/speckit:plan`, `/speckit:tasks`, and `/speckit:implement` complete the work.
- The 3 `[NEEDS CLARIFICATION]` markers (FR-018, FR-019, FR-020) are intentional and respect CLAUDE.md rule 3: `/speckit:clarify` MUST run before `/speckit:plan` for this feature.
- Per the analyze report from feature 002 (process note 1), this feature is the first to actually use the `/speckit:clarify` step properly. Do not skip it.
