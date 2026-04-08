# Implementation Plan: Research and Bootstrap the wa CLI Project

**Branch**: `001-research-bootstrap` | **Date**: 2026-04-06 | **Spec**: [`spec.md`](./spec.md)
**Input**: Feature specification from `/specs/001-research-bootstrap/spec.md`

## Summary

Resolve every "OPEN" architectural question deferred by [`CLAUDE.md`](../../CLAUDE.md), bootstrap the remote repository at `github.com/yolo-labz/wa`, and put a hexagonal directory skeleton + governance file set in place so the very next feature (`002-domain-and-ports`) can write Go code without first re-litigating layout, license, distribution, or plugin transport.

This feature is **documentation-and-ops only**. It writes zero `.go` files; the spec explicitly excludes Go source from scope (FR-015) and defers it to feature 002+. The technical approach is therefore unusual: instead of producing executable code, this plan produces (1) a citation-backed `research.md` resolving every blueprint OPEN question, (2) a public GitHub repository with a hexagonal scaffold and full governance baseline, and (3) the spec/plan/contracts/quickstart artifacts that downstream `/speckit:plan` runs for features 002–007 will inherit context from.

**Status as of plan time**: User Stories 1, 2, and 3 from spec.md are already largely **complete** by virtue of the parallel research-and-bootstrap work that landed on this branch under commits `cfbaab4`, `263d957`, and `35a5513`. This plan formalises what was done and lists the small set of remaining `/speckit:tasks` items for closure.

## Technical Context

**Language/Version**: Markdown + Bash + minimal Go module manifest. The Go toolchain on the development machine is **go 1.26.1** (recorded in `go.mod`); no `.go` source is produced by this feature.
**Primary Dependencies**: `git`, `gh` (authenticated as `phsb5321` with `repo` scope, member of `yolo-labz`), `curl`, `bash`. The downstream feature 003 will add `go.mau.fi/whatsmeow` as the primary runtime dependency; this feature does not pull it.
**Storage**: Git history under `github.com/yolo-labz/wa`. No databases. No state outside the repo.
**Testing**: This feature has no executable code; "tests" are the validation items in [`checklists/requirements.md`](./checklists/requirements.md) (CHK001–CHK033). Future Go testing will use `go test -race`, `golangci-lint`, `govulncheck`, and `testscript` (`rogpeppe/go-internal/testscript`) per the governance dossier in `research.md` §OPEN-Q7.
**Target Platform**: A reader on macOS arm64 (the maintainer's host) or Linux x86_64/arm64 with `git` and a terminal. Future features will target the same OS matrix at the binary level.
**Project Type**: Single-repo Go monorepo (two binaries `cmd/wa`, `cmd/wad`, all internal packages under `internal/`). Hexagonal layout per [`CLAUDE.md`](../../CLAUDE.md) §"Repository layout".
**Performance Goals**: For this feature, "a fresh clone reads README + CLAUDE.md + spec + research and produces a credible one-paragraph project summary in under 15 minutes" (SC-003). Runtime performance goals for the binaries themselves (sub-millisecond `wa send` cold-start after socket connect; daemon idle <30 MB RAM) are recorded in CLAUDE.md and will be enforced from feature 005 onward.
**Constraints**: Apache-2.0 license, public repository under `yolo-labz`, no committed binaries, no committed secrets, no MCP server inside the `wa` codebase (the future `wa-assistant` plugin layer's MCP shim is the only sanctioned exception, lives in a separate repo).
**Scale/Scope**: One maintainer, one personal WhatsApp number, one workstation. The repository is intentionally not architected for multi-tenancy. Features 002–007 are sequenced as separate speckit features and will each have their own spec/plan/tasks set.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

**No constitution exists.** `.specify/memory/constitution.md` is absent on this branch, and `/speckit:constitution` was not run. The blueprint in [`CLAUDE.md`](../../CLAUDE.md) functions as the de-facto constitution for this project: it locks language, architecture, IPC pattern, safety stack, anti-patterns, and license. For this feature, "constitution check" therefore means **"do nothing in this plan that contradicts CLAUDE.md without surfacing the contradiction."**

**Initial gate (Phase 0):** Pass. Three contradictions with the original CLAUDE.md were surfaced during the research phase and resolved explicitly:

1. **License MPL-2.0 → Apache-2.0** — overturned with Mozilla MPL FAQ Q9–Q11 evidence and the Telegram channel plugin precedent. Resolution committed in `35a5513`. CLAUDE.md row updated.
2. **"No MCP server in this repo by design"** — clarified, not contradicted. The rule still binds the `wa` CLI/daemon. The future `wa-assistant` plugin's channel server is an MCP server because Anthropic's Channels feature requires it, but `wa-assistant` is a *separate repo*. CLAUDE.md now names the layering explicitly.
3. **`scripts.postInstall` plugin lifecycle hook** — dropped. Verified against the live Anthropic plugin docs and the official Telegram plugin source (`external_plugins/telegram/.claude-plugin/plugin.json` has only `name`/`description`/`version`/`keywords`). The `wa-assistant` plugin will install binaries via `brew` / `nix profile` / `go install` / a `/wa:install` Bash skill — not via a manifest hook.

**Post-design gate (Phase 1, after this plan + data-model + contracts + quickstart):** Pass. The Phase 1 artifacts only describe what was already produced; they introduce no new architectural decisions and therefore cannot contradict CLAUDE.md.

**Recommendation**: run `/speckit:constitution` before feature 002 starts. The locked decisions in CLAUDE.md are good seed material — convert them into a numbered constitution with explicit "MUST"/"MUST NOT" language so future plan-time gates have something to check against besides prose.

## Project Structure

### Documentation (this feature)

```text
specs/001-research-bootstrap/
├── spec.md              # /speckit:specify output  (committed 263d957)
├── research.md          # /speckit:specify research synthesis  (committed 35a5513)
├── plan.md              # this file  (/speckit:plan)
├── data-model.md        # Phase 1 output  (/speckit:plan)
├── quickstart.md        # Phase 1 output  (/speckit:plan)
├── contracts/
│   ├── research-md.md           # the citation-and-OPEN-Q contract that research.md must satisfy
│   ├── repository-state.md      # the GH repo state contract (visibility, branches, default)
│   └── scaffold-tree.md         # the directory + governance file contract
├── checklists/
│   └── requirements.md  # 33-item validation, 33/33 checked at plan time
└── tasks.md             # /speckit:tasks output  (NOT created by /speckit:plan)
```

### Source Code (repository root)

```text
github.com/yolo-labz/wa/
├── CLAUDE.md                              # the architectural blueprint (~250 lines)
├── README.md                              # public-facing project intro
├── SECURITY.md                            # threat model T1–T7
├── LICENSE                                # Apache-2.0
├── .editorconfig
├── .gitignore                             # Go + secrets + session.db
├── go.mod                                 # github.com/yolo-labz/wa, Go 1.26.1
├── cmd/
│   ├── wa/.gitkeep                        # CLI client entrypoint (feature 005)
│   └── wad/.gitkeep                       # daemon entrypoint (feature 004)
├── internal/
│   ├── domain/.gitkeep                    # JID, Contact, Group, Message, Allowlist  (feature 002)
│   ├── app/.gitkeep                       # use cases + ports.go  (feature 002)
│   └── adapters/
│       ├── primary/socket/.gitkeep        # JSON-RPC server  (feature 004)
│       └── secondary/
│           ├── whatsmeow/.gitkeep         # the real WA adapter  (feature 003)
│           ├── sqlitestore/.gitkeep       # whatsmeow session store wrapper  (feature 003)
│           ├── memory/.gitkeep            # in-memory fakes for tests  (feature 002)
│           └── slogaudit/.gitkeep         # audit log adapter  (feature 002)
├── specs/
│   └── 001-research-bootstrap/            # this directory
└── .specify/                              # speckit scripts + templates
```

**Structure Decision**: hexagonal / ports-and-adapters per [`CLAUDE.md`](../../CLAUDE.md) §"Repository layout". The two-binary split (`cmd/wa` thin client + `cmd/wad` composition root) follows the Tailscale `tailscale` + `tailscaled` pattern. Hexagonal applies only to `wad`; `cmd/wa` is a dumb JSON-RPC client and holds zero use case logic. This decision was locked in CLAUDE.md before this feature began and is not relitigated here.

## Phase 0 — Research

**Status: complete.** The full Phase 0 deliverable lives at [`research.md`](./research.md) and was produced by `/speckit:specify` rather than `/speckit:plan` because the research scope was the *purpose* of this feature, not an input to it. The `research.md` document has 8 OPEN-Q sections (all `[resolved]`) and 37 inline `https://` citations. Zero `NEEDS CLARIFICATION` markers remain across the spec, plan, or research. The plan command's normal Phase 0 workflow ("dispatch research agents for each unknown") therefore short-circuits with no work to do.

For traceability: the five-agent swarm and the two main-session re-runs (Channels API + whatsmeow consumer source) are summarised in `research.md` §"Swarm scope and execution". The two contradictions with the prior blueprint are listed in `research.md` §"Contradicts blueprint" and were resolved in commit `35a5513` along with the LICENSE file swap.

## Phase 1 — Design & Contracts

**Status: produced by this plan command.** Three artifacts land alongside this plan.md:

- [`data-model.md`](./data-model.md) — entities defined in spec.md §"Key Entities" formalized with field lists, relationships, and lifecycle states. No tables, no SQL — these are *content* entities (Open Question, Dossier, Resolution) and *deliverable* entities (Repository, Scaffold), not domain entities for the future Go code (those will be defined in feature 002's data-model.md).
- [`contracts/`](./contracts/) — three filesystem and state contracts that any future replay of this feature must satisfy:
  - `research-md.md` — the structural contract for research.md (sections, citations-per-section, status flags).
  - `repository-state.md` — the GitHub repo state contract (visibility, branches, default branch, license metadata).
  - `scaffold-tree.md` — the on-disk directory and governance-file contract.
- [`quickstart.md`](./quickstart.md) — a literal command sequence a fresh contributor runs to verify every deliverable in under five minutes. This is the executable form of the deliverables checklist (CHK026–CHK033).

**Agent context update** (`update-agent-context.sh`): **skipped intentionally**. The script's purpose is to maintain an auto-generated CLAUDE.md by parsing plan.md fields (`Language/Version`, `Primary Dependencies`, etc.) and merging them into a template that has `<!-- MANUAL ADDITIONS START/END -->` markers. This project's `CLAUDE.md` is hand-authored, lacks those markers, is the architectural source of truth, and is referenced from research.md, README.md, SECURITY.md, and the spec. Running the auto-update script would either silently no-op or destructively overwrite hand-curated content. The right time to introduce it is feature 002, as a planned chore to convert CLAUDE.md to the marker format if and only if speckit auto-updates start delivering value. **Action**: leave CLAUDE.md alone until then.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

There are no constitution violations to justify. The three CLAUDE.md contradictions surfaced in §"Constitution Check" are documented adjustments to the prior blueprint with cited evidence, not new complexity introduced by this plan. The plan adds zero source files, zero dependencies, zero subsystems. Complexity tracking is therefore N/A for this feature.

## Followup wiring for `/speckit:tasks`

When `/speckit:tasks` is run against this plan, the resulting `tasks.md` should produce a small set of confirmation-and-cleanup tasks rather than a build plan, because almost all of feature 001's work is already on disk and pushed:

| Task | Status today | Notes |
|---|---|---|
| Resolve OPEN questions in research.md | done | 8/8, committed `35a5513` |
| Bootstrap GitHub repo | done | `yolo-labz/wa` public, `main` default |
| Hexagonal scaffold + .gitkeep placeholders | done | `find cmd internal -type d` matches CLAUDE.md exactly |
| LICENSE = Apache-2.0 | done | committed `35a5513` |
| README.md, SECURITY.md, .gitignore, .editorconfig | done | committed |
| go.mod initialized | done | `github.com/yolo-labz/wa`, Go 1.26.1 |
| Spec validation (CHK001–CHK025) | done | all checked |
| Deliverables validation (CHK026–CHK033) | done | all checked at plan time |
| `plan.md`, `data-model.md`, `contracts/`, `quickstart.md` | landing in this commit | this `/speckit:plan` run |
| Run `/speckit:constitution` to lock decisions formally | not done | recommended before feature 002 starts |
| Lift governance dossier files (`.golangci.yml`, `cliff.toml`, `renovate.json`, `lefthook.yml`, `.github/workflows/ci.yml`) into the repo as a chore commit | not done | scheduled for feature 002 or earlier — they cannot break CI on a no-Go repo |

The expected `/speckit:tasks` output for this feature is therefore short: ~5 tasks, mostly bookkeeping (run constitution; lift governance configs; final push), with the main implementation work (002+) tracked under separate features.

## Artifacts produced by this plan

- [`plan.md`](./plan.md) — this file
- [`data-model.md`](./data-model.md) — entity definitions
- [`contracts/research-md.md`](./contracts/research-md.md)
- [`contracts/repository-state.md`](./contracts/repository-state.md)
- [`contracts/scaffold-tree.md`](./contracts/scaffold-tree.md)
- [`quickstart.md`](./quickstart.md) — five-minute verification runbook
