---
description: "Task list for feature 001-research-bootstrap"
---

# Tasks: Research and Bootstrap the wa CLI Project

**Input**: Design documents from `/specs/001-research-bootstrap/`
**Prerequisites**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`contracts/`](./contracts/), [`quickstart.md`](./quickstart.md)

**Tests**: Not applicable — this feature produces no executable code. "Validation" means running [`quickstart.md`](./quickstart.md) and ticking [`checklists/requirements.md`](./checklists/requirements.md) and [`checklists/architecture.md`](./checklists/architecture.md).

**Organization**: Tasks are grouped by user story from spec.md (US1 = research; US2 = remote repo; US3 = scaffold). Almost everything is **already complete** as of commit `034c81e` and the second pass following "solve everything"; this file is a bookkeeping record more than a build plan.

## Format: `[ID] [P?] [Story] Description — Status — Commit/path`

- **[P]**: Could run in parallel — for this feature, almost everything is sequential or already done.
- **[Story]**: US1 (research) · US2 (remote repo) · US3 (scaffold) · GOV (governance/cross-cutting).

---

## Phase 1: Setup (Shared infrastructure)

- [x] T001 [GOV] Initialize git repo, set `main` branch, configure user identity — done — commit `cfbaab4`
- [x] T002 [GOV] Install `.specify/` template tree from spec-kit v0.4.4 release — done — commit `cfbaab4`
- [x] T003 [GOV] Run `bash .specify/scripts/bash/create-new-feature.sh --json --number 1 --short-name research-bootstrap` to create branch and spec stub — done — branch `001-research-bootstrap`
- [x] T004 [GOV] `go mod init github.com/yolo-labz/wa` — done — `go.mod`

## Phase 2: Foundational (Blocking prerequisites)

- [x] T005 [GOV] Write [`CLAUDE.md`](../../CLAUDE.md) architectural blueprint with locked decisions, port interfaces, daemon model, safety stack, anti-patterns, references — done — commit `cfbaab4`, revised `034c81e+1`
- [x] T006 [GOV] Author [`spec.md`](./spec.md) with three prioritized user stories (US1 research, US2 remote repo, US3 scaffold), 15 functional requirements, key entities, success criteria, assumptions — done — commit `263d957`
- [x] T007 [GOV] Author [`checklists/requirements.md`](./checklists/requirements.md) with 33 items (CHK001–CHK025 spec quality, CHK026–CHK033 deliverables) — done — commit `263d957`

## Phase 3: User Story 1 — Research

**Goal**: Resolve every OPEN architectural question with cited evidence.
**Independent test**: `grep '^## OPEN-Q' specs/001-research-bootstrap/research.md | wc -l` ≥ 5; every section has ≥ 1 inline `https://` citation.

- [x] T008 [US1] Deploy five parallel deep-research agents covering: language/framework, hexagonal architecture, daemon/IPC, plugin wrapping, design checklist — done
- [x] T009 [US1] Deploy second swarm (after CLAUDE.md) for: Channels API live verify, whatsmeow pairing UX, GoReleaser/notarization, license/governance, whatsmeow consumer source patterns — done
- [x] T010 [US1] Re-verify Channels API in main session via `mcp__plugin_context-mode_context-mode__ctx_fetch_and_index` after agent reported false negative — done; Channels confirmed real
- [x] T011 [US1] Clone `mautrix/whatsapp` and `aldinokemal/go-whatsapp-web-multidevice` locally; extract production whatsmeow setup pattern (`pkg/connector/client.go` lines 75–87) and the 3-minute detached QR context fix (`src/usecase/app.go` lines 40–160) — done
- [x] T012 [US1] Clone `anthropics/claude-plugins-official`; read `external_plugins/telegram/{plugin.json,.mcp.json,server.ts,skills/access/SKILL.md}` to nail down the wa-assistant template — done
- [x] T013 [US1] Synthesize [`research.md`](./research.md) with 8 OPEN-Q sections, 37 inline citations, "Contradicts blueprint" table, plugin design (revised) section, followups list, consolidated sources — done — commit `35a5513`
- [x] T014 [US1] Update CLAUDE.md to resolve the three contradictions surfaced in research (license MPL→Apache, MCP layering clarification, drop `scripts.postInstall`) — done — commit `35a5513`

## Phase 4: User Story 2 — Remote repository

**Goal**: `github.com/yolo-labz/wa` exists, public, default `main`, both branches pushed.
**Independent test**: `gh repo view yolo-labz/wa --json visibility,defaultBranchRef | jq -e '.visibility=="PUBLIC" and .defaultBranchRef.name=="main"'`.

- [x] T015 [US2] Verify `gh auth status` has `repo` scope and `gh api orgs/yolo-labz` returns HTTP 200 — done
- [x] T016 [US2] `gh repo create yolo-labz/wa --public --description … --source . --remote origin --push` — done
- [x] T017 [US2] Push `main` branch and `gh repo edit --default-branch main` — done
- [x] T018 [US2] Push `001-research-bootstrap` feature branch — done — commit `355038f` and beyond

## Phase 5: User Story 3 — Scaffold and governance

**Goal**: Hexagonal directory tree + governance file set + Go module ready for feature 002 to start writing code.
**Independent test**: `find cmd internal -type d` matches CLAUDE.md tree exactly; `go mod tidy && go vet ./...` exits 0; all required files present.

- [x] T019 [US3] Create hexagonal directory skeleton with `.gitkeep` placeholders (`cmd/{wa,wad}`, `internal/{domain,app,adapters/{primary/socket,secondary/{whatsmeow,sqlitestore,memory,slogaudit}}}`) — done — commit `263d957`
- [x] T020 [US3] Write `LICENSE` (Apache-2.0), `README.md`, `SECURITY.md` (T1–T7 threat model), `.gitignore`, `.editorconfig` — done — commit `263d957`, LICENSE swapped in `35a5513`
- [x] T021 [US3] Write [`plan.md`](./plan.md) with technical context, constitution check, project structure, complexity tracking — done — commit `355038f`
- [x] T022 [US3] Write [`data-model.md`](./data-model.md) (Open Question, Dossier, Resolution, Repository, Scaffold, Plan Artifact Set) — done — commit `355038f`
- [x] T023 [US3] [P] Write [`contracts/research-md.md`](./contracts/research-md.md), [`contracts/repository-state.md`](./contracts/repository-state.md), [`contracts/scaffold-tree.md`](./contracts/scaffold-tree.md) — done — commit `355038f`
- [x] T024 [US3] Write [`quickstart.md`](./quickstart.md) — done — commit `355038f`
- [x] T025 [US3] Write [`checklists/architecture.md`](./checklists/architecture.md) (50-item decision-quality audit) — done — commit `034c81e`

## Phase 6: Solve-everything cleanup pass

- [x] T026 [GOV] Walk all 50 architecture checklist items, identify documentation defects in CLAUDE.md, apply ~15 targeted Edits (Go pin, whatsmeow pin, CGO rule, depguard explicit, JSON-RPC schema, clientCtx rule, wire-format alternatives, 3-min QR context, re-pair hint responsibility, untrusted-sender Telegram precedent, wa-assistant file tree + state path + anti-injection rule, governance toolchain row, v0 testing strategy, all 8 OPEN-Q enumerated) — done
- [x] T027 [GOV] Tick all 50 items in `checklists/architecture.md` after fixes — done
- [x] T028 [GOV] [P] Save research dossiers durably under `docs/research-dossiers/{distribution.md,governance.md}` so feature 006 / feature 002 can lift configs without re-deriving — done
- [x] T029 [GOV] [P] Lift governance configs into the repo root: `.golangci.yml` (with depguard `core-no-whatsmeow` rule), `cliff.toml`, `renovate.json`, `lefthook.yml`, `.github/workflows/ci.yml` — done
- [x] T030 [GOV] Author [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md) v1.0.0 with seven core principles formalised from CLAUDE.md locked decisions — done
- [x] T031 [GOV] Generate this `tasks.md` (the file you are reading) — done
- [x] T032 [GOV] Tick all deliverables in `checklists/requirements.md` (CHK026–CHK033) — already done in commit `35a5513`
- [ ] T033 [GOV] Final commit "feat(governance): solve-everything cleanup" and push to `001-research-bootstrap` — pending
- [ ] T034 [GOV] Tag `v0.0.1-research-bootstrap` (annotated) marking the end of feature 001 — pending
- [ ] T035 [GOV] Open a milestone or tracking issue for features 002–007 on `yolo-labz/wa` — optional, nice-to-have

## Dependencies

- T001 → T002 → T003 → T004 → all later tasks
- T005 (CLAUDE.md) → T006 (spec) → T007 (checklist)
- US1 tasks (T008–T014) can interleave with US2/US3 since the research swarm runs in background
- US2 (T015–T018) and US3 (T019–T025) can interleave; both depend on T004 and CLAUDE.md
- Phase 6 (T026–T035) depends on all previous phases

## Parallel example

Phase 6 tasks T028, T029 are marked `[P]` because they touch disjoint paths (`docs/research-dossiers/` vs root config files). They were committed together in this session.

## Implementation strategy

This feature is documentation-and-ops; the "implementation" is `Write` and `Edit` calls plus `git`/`gh`. The natural commit boundaries are:

1. `cfbaab4` — bootstrap blueprint + speckit scaffold (T001–T005)
2. `263d957` — spec, scaffold, governance baseline (T006–T007, T019, T020)
3. `35a5513` — research synthesis + license swap + CLAUDE.md contradiction fixes (T008–T014, partial T020)
4. `355038f` — plan, data-model, contracts, quickstart (T021–T024)
5. `034c81e` — architecture checklist (T025)
6. *next commit* — solve-everything cleanup (T026–T034)

**MVP scope** is everything through commit `355038f`. The "solve everything" pass (T026–T034) is hardening — not strictly required to declare US1/US2/US3 met, but required to declare the deliverables checklist 33/33 and the architecture checklist 50/50.

## Out of scope (deferred to feature 002+)

- Writing any `*.go` source file under `cmd/` or `internal/` — all of feature 001's directories are intentionally `.gitkeep`-only.
- Implementing any port interface — the seven interfaces in CLAUDE.md §"Ports" are signatures only.
- Wiring `wad` composition root, daemon process, or unix socket server.
- Pairing a real WhatsApp number.
- Building the `wa-assistant` Claude Code plugin.
- Notarizing or releasing any binary.
