# Requirements Checklist: Research and Bootstrap the wa CLI Project

**Purpose**: Validate that `specs/001-research-bootstrap/spec.md` meets speckit content-quality, requirement-completeness, and feature-readiness criteria, and that the deliverables described by the spec actually exist in the repository at feature close.
**Created**: 2026-04-06
**Feature**: [spec.md](../spec.md)

## Content quality

- [x] CHK001 Spec is written for stakeholders, not implementers (no Go syntax, no API field names, no library internals in user stories or success criteria)
- [x] CHK002 Every section in `.specify/templates/spec-template.md` that applies to this feature is filled out; sections that do not apply have been removed entirely
- [x] CHK003 No more than 3 `[NEEDS CLARIFICATION: ...]` markers remain in the spec
- [x] CHK004 The "Input" line at the top of the spec quotes the user's actual `/speckit:specify` arguments
- [x] CHK005 The "Status" line is set to `Draft` (not `Approved` until the maintainer signs off)
- [x] CHK006 No embedded checklists inside the spec body — checklists live only in this file

## Requirement completeness

- [x] CHK007 Every functional requirement is testable: a developer or QA reading FR-NNN can write a pass/fail test from it without inventing assumptions
- [x] CHK008 Every functional requirement maps to at least one user story or edge case
- [x] CHK009 Every user story has an "Independent Test" sentence describing how to verify it in isolation
- [x] CHK010 Every user story has at least one Given/When/Then acceptance scenario
- [x] CHK011 Every Key Entity referenced in the requirements appears in the Key Entities section with a definition
- [x] CHK012 Every assumption is recorded under Assumptions; nothing load-bearing is implicit
- [x] CHK013 Edge cases enumerate at least three failure or boundary conditions
- [x] CHK014 The spec covers all of: research output, repo creation, scaffold creation, integration with prior CLAUDE.md blueprint

## Success criteria quality

- [x] CHK015 Each SC-NNN is measurable (cites a number, percentage, time bound, or yes/no observable)
- [x] CHK016 Each SC-NNN is technology-agnostic (no mention of Go, whatsmeow, JSON-RPC, cobra, GoReleaser, etc.)
- [x] CHK017 Each SC-NNN is verifiable from outside the implementation (a third party can run the check)
- [x] CHK018 Success criteria collectively cover both the research deliverable and the bootstrap deliverable

## Feature readiness

- [x] CHK019 User stories are prioritized (P1/P2/P3) and the priorities reflect actual blocking relationships
- [x] CHK020 The spec identifies what is in scope and what is explicitly out of scope (writing Go source code is out of scope for this feature)
- [x] CHK021 The spec lists the files that must exist when the feature is complete (research.md, scaffold dirs, governance files, remote repo)
- [x] CHK022 The spec does not relitigate decisions already locked in CLAUDE.md unless evidence demands it
- [x] CHK023 The spec accounts for the failure mode where a research agent's web fetch fails (UNVERIFIED marker requirement)
- [x] CHK024 The spec accounts for the failure mode where the GitHub repo already exists
- [x] CHK025 The spec accounts for the failure mode where research contradicts the blueprint

## Deliverables present (re-run at feature close)

- [ ] CHK026 `specs/001-research-bootstrap/research.md` exists and contains one section per OPEN question
- [ ] CHK027 Every section in `research.md` has at least one inline `https://` citation
- [ ] CHK028 The remote repository `github.com/yolo-labz/wa` exists and is reachable via `gh repo view`
- [ ] CHK029 The repo's default branch contains `CLAUDE.md`, `.specify/`, `LICENSE`, `.gitignore`, `README.md`, `SECURITY.md`, `go.mod`
- [ ] CHK030 The hexagonal directory skeleton exists: `cmd/wa`, `cmd/wad`, `internal/domain`, `internal/app`, `internal/adapters/primary`, `internal/adapters/secondary`
- [ ] CHK031 `go mod tidy` and `go vet ./...` both succeed in the repo with zero errors
- [ ] CHK032 The `001-research-bootstrap` branch is pushed to `origin` and is visible on GitHub
- [ ] CHK033 `git status` is clean at feature close

## Notes

- Items CHK001–CHK025 validate the spec itself and were checked during spec authoring.
- Items CHK026–CHK033 validate the deliverables and must be re-run after research synthesis, scaffolding, and `gh repo create` complete.
- If any item under "Deliverables present" cannot be checked, the reason must be documented in a follow-up note here, never silently left blank.
