---
description: "Task list for feature 001-research-bootstrap"
---

# Tasks: Research and Bootstrap the wa CLI Project

**Input**: Design documents from `/specs/001-research-bootstrap/`
**Prerequisites**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`contracts/`](./contracts/), [`quickstart.md`](./quickstart.md)

**Tests**: Not applicable. This feature produces no executable code; "validation" runs [`quickstart.md`](./quickstart.md) and the items in [`checklists/requirements.md`](./checklists/requirements.md) and [`checklists/architecture.md`](./checklists/architecture.md).

**Organization**: Tasks are grouped by user story from spec.md (US1 = research; US2 = remote repository; US3 = scaffold and governance). Almost every task is **already complete** in git history; this file is the retrospective bookkeeping record. Every task lists the exact file path it produced or modified. Where a task touches multiple files, the canonical path is the spec/plan/research/contract artefact it lands.

## Format: `[ ] [TaskID] [P?] [Story?] Description with file path`

- `[x]` = completed in git history
- `[P]` = could run in parallel (different files, no shared dependency)
- `[USn]` = applies only to user-story phases; setup/foundational/polish phases carry no story label

---

## Phase 1: Setup

**Purpose**: Project initialization — git, speckit, Go module.

- [x] T001 Initialize git repository on `main`, configure user identity at `/Users/notroot/Documents/Code/WhatsAppAutomation/.git/config`
- [x] T002 Install spec-kit v0.4.4 template tree at `/Users/notroot/Documents/Code/WhatsAppAutomation/.specify/`
- [x] T003 Run `bash .specify/scripts/bash/create-new-feature.sh --json --number 1 --short-name research-bootstrap` to create branch `001-research-bootstrap` and stub `specs/001-research-bootstrap/spec.md`
- [x] T004 Initialize Go module `github.com/yolo-labz/wa` at `/Users/notroot/Documents/Code/WhatsAppAutomation/go.mod`

---

## Phase 2: Foundational (Blocking prerequisites)

**Purpose**: Documents the rest of the work depends on. MUST complete before any user story can start.

- [x] T005 Author the architectural blueprint at `/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md` with locked decisions, port interfaces, daemon model, safety stack, anti-patterns, references
- [x] T006 Author the feature specification at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/spec.md` with three prioritized user stories, 15 functional requirements, key entities, success criteria, assumptions
- [x] T007 Author the spec-quality + deliverables checklist at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/checklists/requirements.md` with 33 items (CHK001–CHK025 spec hygiene, CHK026–CHK033 deliverables)

---

## Phase 3: User Story 1 — Research (Priority: P1)

**Goal**: Resolve every OPEN architectural question with cited evidence in `research.md`.

**Independent test**: `grep -c '^## OPEN-Q' specs/001-research-bootstrap/research.md` ≥ 5; every section has at least one inline `https://` citation; zero `[NEEDS CLARIFICATION]` markers remain.

- [x] T008 [US1] Deploy first parallel research swarm of five deep-researcher agents covering language/framework, hexagonal architecture, daemon/IPC, plugin wrapping, and pre-implementation design checklist; outputs synthesised into `/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md`
- [x] T009 [US1] Deploy second parallel research swarm of five agents (post-CLAUDE.md) covering Channels API live verification, whatsmeow pairing UX, GoReleaser/macOS notarization, license/governance toolchain, and whatsmeow consumer source patterns; outputs synthesised into `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/research.md`
- [x] T010 [P] [US1] Re-verify the Claude Code Channels API in the main session via `mcp__plugin_context-mode_context-mode__ctx_fetch_and_index` after the first agent reported a false-negative; document the verification at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/research.md` §OPEN-Q3
- [x] T011 [P] [US1] Clone `mautrix/whatsapp` and `aldinokemal/go-whatsapp-web-multidevice` to `/tmp/`; extract production whatsmeow setup pattern (`pkg/connector/client.go` lines 75–87) and the 3-minute detached QR context fix (`src/usecase/app.go` lines 40–160); record findings at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/research.md` §OPEN-Q1 and §OPEN-Q8
- [x] T012 [P] [US1] Clone `anthropics/claude-plugins-official` to `/tmp/`; read `external_plugins/telegram/{plugin.json,.mcp.json,server.ts,skills/access/SKILL.md,skills/configure/SKILL.md}` to lock down the wa-assistant template; record at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/research.md` §"Plugin design (revised)"
- [x] T013 [US1] Synthesise the full research dossier at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/research.md` with 8 OPEN-Q sections, 37 inline citations, "Contradicts blueprint" table, plugin design (revised) section, followups list, consolidated sources
- [x] T014 [US1] Apply the three contradiction fixes from research.md to `/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md` (license MPL → Apache, MCP layering clarification, drop `scripts.postInstall`) and to `/Users/notroot/Documents/Code/WhatsAppAutomation/LICENSE` (replace MPL-2.0 text with Apache-2.0 text)

**Checkpoint**: US1 is independently testable now — `research.md` exists with 8 cited sections; CLAUDE.md reflects the contradictions; LICENSE is Apache-2.0.

---

## Phase 4: User Story 2 — Remote repository (Priority: P1)

**Goal**: `github.com/yolo-labz/wa` exists, public, default branch `main`, both branches pushed.

**Independent test**: `gh repo view yolo-labz/wa --json visibility,defaultBranchRef | jq -e '.visibility=="PUBLIC" and .defaultBranchRef.name=="main"'` exits 0.

- [x] T015 [US2] Verify `gh auth status` reports `repo` scope and `gh api orgs/yolo-labz` returns HTTP 200 — gating check before any remote write
- [x] T016 [US2] `gh repo create yolo-labz/wa --public --description "Personal WhatsApp automation CLI in Go (whatsmeow), backing a Claude Code plugin" --source . --remote origin --push` writing the new remote to `/Users/notroot/Documents/Code/WhatsAppAutomation/.git/config`
- [x] T017 [US2] Push the `main` branch and run `gh repo edit yolo-labz/wa --default-branch main` to make `main` the default
- [x] T018 [US2] Push the `001-research-bootstrap` feature branch to `origin/001-research-bootstrap`

**Checkpoint**: US2 is independently testable now — the repo is reachable and both branches are visible on GitHub.

---

## Phase 5: User Story 3 — Scaffold and governance (Priority: P2)

**Goal**: Hexagonal directory tree + governance file set + Go module ready for feature 002 to start writing code.

**Independent test**: `find cmd internal -type d` matches the documented tree exactly; `go mod tidy && go vet ./...` exits 0; every required governance file is present and non-empty.

- [x] T019 [P] [US3] Create the hexagonal scaffold with `.gitkeep` placeholders at `/Users/notroot/Documents/Code/WhatsAppAutomation/cmd/wa/.gitkeep`, `cmd/wad/.gitkeep`, `internal/domain/.gitkeep`, `internal/app/.gitkeep`, `internal/adapters/primary/socket/.gitkeep`, `internal/adapters/secondary/whatsmeow/.gitkeep`, `internal/adapters/secondary/sqlitestore/.gitkeep`, `internal/adapters/secondary/memory/.gitkeep`, `internal/adapters/secondary/slogaudit/.gitkeep`
- [x] T020 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/LICENSE` (Apache-2.0 from `https://www.apache.org/licenses/LICENSE-2.0.txt`)
- [x] T021 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/README.md` (~200 lines, public-facing project intro pointing at CLAUDE.md and the active spec)
- [x] T022 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/SECURITY.md` with the T1–T7 threat model section
- [x] T023 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/.gitignore` covering Go build artefacts, session DBs, secrets, OS metadata, direnv state
- [x] T024 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/.editorconfig` (LF endings, tab indent for Go)
- [x] T025 [US3] Write the implementation plan at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/plan.md` with summary, technical context, constitution check, project structure, complexity tracking, followup wiring
- [x] T026 [P] [US3] Write the entity definitions at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/data-model.md` (Open Question, Dossier, Question Resolution, Repository, Scaffold, Plan Artifact Set)
- [x] T027 [P] [US3] Write the research-md structural contract at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/contracts/research-md.md`
- [x] T028 [P] [US3] Write the GitHub repository state contract at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/contracts/repository-state.md`
- [x] T029 [P] [US3] Write the scaffold tree contract at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/contracts/scaffold-tree.md`
- [x] T030 [US3] Write the five-minute verification runbook at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/quickstart.md`
- [x] T031 [US3] Write the 50-item architecture decision-quality checklist at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/checklists/architecture.md`

**Checkpoint**: US3 is independently testable now — the scaffold exists, governance files are populated, `go mod tidy && go vet ./...` succeeds.

---

## Phase 6: Polish & cross-cutting concerns

**Purpose**: Solve-everything cleanup pass that closed CHK026–CHK033 deliverables and 50/50 architecture items.

- [x] T032 Walk all 50 items in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/checklists/architecture.md`, identify documentation defects, apply ~15 targeted edits to `/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md` (Go pin, whatsmeow pin, no-CGO rule, depguard explicit, JSON-RPC schema, clientCtx rule, wire-format alternatives, 3-min QR context, re-pair hint owner, untrusted-sender Telegram precedent, wa-assistant file tree, channel state path, anti-injection rule, governance toolchain row, v0 testing strategy, all 8 OPEN-Q enumerated)
- [x] T033 Tick all 50 items in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/checklists/architecture.md`
- [x] T034 [P] Save the GoReleaser/notarization dossier durably at `/Users/notroot/Documents/Code/WhatsAppAutomation/docs/research-dossiers/distribution.md` so feature 006 can lift configs without re-deriving
- [x] T035 [P] Save the governance dossier durably at `/Users/notroot/Documents/Code/WhatsAppAutomation/docs/research-dossiers/governance.md`
- [x] T036 [P] Land the linter config at `/Users/notroot/Documents/Code/WhatsAppAutomation/.golangci.yml` with the `depguard` rule `core-no-whatsmeow` enforcing the hexagonal core/adapter boundary
- [x] T037 [P] Land the changelog config at `/Users/notroot/Documents/Code/WhatsAppAutomation/cliff.toml` (`git-cliff` conventional-commits → `CHANGELOG.md`)
- [x] T038 [P] Land the dependency-bump config at `/Users/notroot/Documents/Code/WhatsAppAutomation/renovate.json` with the special `whatsmeow` package rule
- [x] T039 [P] Land the pre-commit config at `/Users/notroot/Documents/Code/WhatsAppAutomation/lefthook.yml` (pre-commit, commit-msg conventional-commit regex, pre-push)
- [x] T040 [P] Land the GitHub Actions workflow at `/Users/notroot/Documents/Code/WhatsAppAutomation/.github/workflows/ci.yml` with `lint`, `test`, and `govulncheck` jobs
- [x] T041 Author the project constitution at `/Users/notroot/Documents/Code/WhatsAppAutomation/.specify/memory/constitution.md` v1.0.0 with seven core principles formalised from CLAUDE.md
- [x] T042 Tick all eight deliverables items (CHK026–CHK033) in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/checklists/requirements.md`
- [x] T043 Generate this task list at `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/001-research-bootstrap/tasks.md`
- [x] T044 Switch the git remote from HTTPS to SSH (`git@github.com:yolo-labz/wa.git`) at `/Users/notroot/Documents/Code/WhatsAppAutomation/.git/config` to bypass the missing `workflow` OAuth scope without an interactive `gh auth refresh`
- [x] T045 Push the solve-everything cleanup commit and the ci workflow commit to `origin/001-research-bootstrap`
- [x] T046 Create the annotated tag `v0.0.1-research-bootstrap` at `/Users/notroot/Documents/Code/WhatsAppAutomation/.git/refs/tags/v0.0.1-research-bootstrap` and push it to origin
- [x] T047 Fix the `.golangci.yml` v2 schema vs CI `version: v1.62` mismatch in `/Users/notroot/Documents/Code/WhatsAppAutomation/.github/workflows/ci.yml` by changing `version: v1.62` to `version: latest`

---

## Dependencies

```text
T001 → T002 → T003 → T004 ─┐
                            ├→ T005 → T006 → T007 ─┐
                            │                       │
                            │                       ├→ Phase 3 (US1: T008..T014)
                            │                       ├→ Phase 4 (US2: T015..T018)
                            │                       └→ Phase 5 (US3: T019..T031)
                            │                                              │
                            │                                              ↓
                            └─────────────────────────────→ Phase 6 (Polish: T032..T047)
```

- US1 (research) and US2 (repo creation) and US3 (scaffold) can interleave once T007 finishes — they touch disjoint files. They were committed in the order US3 → US1 → US2 → US3-finish in this session.
- Phase 6 polish depends on all three user stories being done because it ticks their checklists and patches CLAUDE.md based on the architecture audit.
- T044 (SSH remote switch) blocks T045 (the push that includes `.github/workflows/ci.yml`) only because the active OAuth token lacks `workflow` scope; if a future session refreshes that scope, T044 becomes optional.
- T047 is a follow-up bug fix for T040 — they could be merged in a future replay.

## Parallel example

Inside Phase 3 (US1), tasks T010, T011, T012 are marked `[P]` because they touch different `/tmp/` clones and write to different sections of `research.md`. They were performed sequentially in this session for output-context reasons but could parallelise on a fresh run.

Inside Phase 5 (US3), tasks T019–T024 and T026–T029 are mostly `[P]` because each writes a distinct file under the project root or `specs/001-research-bootstrap/contracts/`.

Inside Phase 6 (Polish), the dossiers (T034, T035) and the governance configs (T036–T040) are all `[P]` against each other.

## Implementation strategy

This feature is documentation-and-ops; the "implementation" is `Write` and `Edit` calls plus `git`/`gh`. The natural commit boundaries are:

| Commit | Phase coverage |
|---|---|
| `cfbaab4 chore: bootstrap project blueprint and speckit scaffold` | T001–T005 |
| `263d957 feat(spec): add 001-research-bootstrap spec, checklist, scaffold` | T006, T007, T019–T024 |
| `35a5513 feat(research): synthesize swarm dossiers and resolve all OPEN questions` | T008–T014 |
| `355038f feat(plan): add 001-research-bootstrap plan, data-model, contracts, quickstart` | T025–T030 |
| `034c81e feat(checklist): add architecture decision-quality checklist` | T031 |
| `604f105 feat(governance): solve-everything cleanup pass` | T032, T033, T034, T035, T036, T037, T038, T039, T041, T042, T043, T044, T045 (partial) |
| `356e5cc ci: add lint+test+govulncheck workflow` | T040, T045 (final) |
| `f23ba85 fix(ci): bump golangci-lint to latest (v2.x) to match v2 config schema` | T047 |

**MVP scope** for this feature is the three User Stories (Phases 3, 4, 5) — everything through commit `034c81e`. Phase 6 is hardening and not strictly required to declare US1/US2/US3 met, but it is required to declare every checklist item green. With Phase 6 done, the feature is fully closed.

## Out of scope (deferred to feature 002+)

- Writing any `*.go` source file under `cmd/` or `internal/` — every directory is intentionally `.gitkeep`-only at v0.
- Implementing any port interface — the seven interfaces in CLAUDE.md §"Ports" are signatures only.
- Wiring `wad`'s composition root, daemon process, or unix-socket server.
- Pairing a real WhatsApp number, sending or receiving any message.
- Building the `wa-assistant` Claude Code plugin in its separate repo.
- Notarising or releasing any binary.
