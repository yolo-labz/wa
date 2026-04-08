# Data Model: Research and Bootstrap

**Branch**: `001-research-bootstrap` · **Plan**: [`plan.md`](./plan.md) · **Spec**: [`spec.md`](./spec.md)

This feature has no executable code and therefore no runtime data model in the conventional sense. The "entities" listed below are **content and deliverable entities** — the things this feature produces and validates. Real Go domain types (`JID`, `Contact`, `Message`, `Allowlist`, etc.) are out of scope here and will be specified in feature 002's `data-model.md`.

## Entity overview

```text
                ┌──────────────────────┐
                │   Open Question      │
                │   (e.g. OPEN-Q1)     │
                └──────────┬───────────┘
                           │ resolved by
                           ▼
                ┌──────────────────────┐
                │  Question Resolution │──── cites ──▶  Source URL
                └──────────┬───────────┘
                           │ aggregated into
                           ▼
                ┌──────────────────────┐
                │     research.md      │◀── synthesized from ──┐
                └──────────────────────┘                       │
                                                               │
                                                       ┌───────┴───────┐
                                                       │    Dossier    │  (one per agent)
                                                       └───────────────┘

                ┌──────────────────────┐
                │     Repository       │──── contains ──▶ Branch (main, 001-research-bootstrap)
                │  github.com/         │
                │  yolo-labz/wa        │──── contains ──▶ Scaffold
                └──────────────────────┘
                           │
                           │ owns
                           ▼
                ┌──────────────────────┐
                │  Governance Files    │  (LICENSE, README, SECURITY, .gitignore, .editorconfig, go.mod)
                └──────────────────────┘
```

## Entities

### 1. Open Question

Represents an architectural decision the prior `CLAUDE.md` blueprint deferred.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `OPEN-Qn` where `n` is sequential, e.g. `OPEN-Q1` through `OPEN-Q8` |
| `title` | string | Short imperative phrase, e.g. "Pairing default flow" |
| `wording` | string | The precise question as it appeared in CLAUDE.md before resolution |
| `default` | string \| null | The provisional answer the blueprint carried, if any |
| `priority` | enum `{scope, security, ux, technical}` | Used to rank `[NEEDS CLARIFICATION]` markers if more than 3 ever appear |

**Lifecycle**: `pending` → `researched` → `resolved` *or* `unverified` *or* `deferred`. All eight Open Questions for this feature are in `resolved` at plan time.

**Source of truth**: the OPEN questions section of [`CLAUDE.md`](../../CLAUDE.md) (which has now been overwritten with the resolution summary; the original list is preserved in git history at commit `cfbaab4`).

---

### 2. Dossier

Represents the output of a single research agent in the swarm.

| Field | Type | Notes |
|---|---|---|
| `agent_id` | string | The opaque background-task ID returned by the Agent tool |
| `topic` | string | The non-overlapping research scope, e.g. "Channels API live verification" |
| `agent_type` | enum `{deep-researcher, Explore, general-purpose}` | All five used `deep-researcher` |
| `status` | enum `{completed, refused, redone-by-main-session}` | Two were refused due to web-fetch sandboxing and re-done by the main session |
| `output_path` | string | `/private/tmp/.../tasks/<agent-id>.output` (volatile; not committed) |
| `citation_count` | integer | Number of `https://` URLs in the dossier |
| `verbatim_blocks` | integer | Number of `> "..."` quoted excerpts from primary sources |

**Lifecycle**: `pending` → `running` → (`completed` ∨ `refused`). On `refused`, the main session may add a `redone-by-main-session` flag and write a replacement using `ctx_fetch_and_index` and/or `git clone` + `Read`.

---

### 3. Question Resolution

The "row" that connects an Open Question to its answer. Lives inline in `research.md`.

| Field | Type | Notes |
|---|---|---|
| `question_id` | string | FK to `Open Question.id` |
| `answer` | string | The recommended answer in one sentence |
| `evidence_urls` | list[string] | At least one `https://` URL must be present |
| `rationale` | string | The reasoning that connects evidence to recommendation |
| `status` | enum `{resolved, unverified, contradicts-blueprint}` | Resolved = accept; unverified = revisit before downstream features depend on it; contradicts-blueprint = surface in research.md §"Contradicts blueprint" and force a CLAUDE.md update |
| `dossier_ids` | list[string] | Which dossiers contributed evidence (may be empty if main session re-derived) |

**Validation rule**: every Resolution with `status = resolved` MUST have `len(evidence_urls) >= 1`. Enforced by spec FR-002 and checklist item CHK027.

---

### 4. Repository

The remote GitHub project produced by this feature.

| Field | Type | Value at feature close |
|---|---|---|
| `owner` | string | `yolo-labz` |
| `name` | string | `wa` |
| `url` | string | `https://github.com/yolo-labz/wa` |
| `visibility` | enum `{public, private}` | `public` |
| `default_branch` | string | `main` |
| `description` | string | "Personal WhatsApp automation CLI in Go (whatsmeow), backing a Claude Code plugin" |
| `branches` | list[string] | `["main", "001-research-bootstrap"]` |
| `license_spdx` | string | `Apache-2.0` |

**Lifecycle**: `nonexistent` → `created` → `populated` → `pushed`. All four states reached.

---

### 5. Scaffold

The on-disk directory + governance file set inside the Repository.

| Field | Type | Notes |
|---|---|---|
| `directories` | list[path] | Must include `cmd/wa`, `cmd/wad`, `internal/domain`, `internal/app`, `internal/adapters/primary/socket`, `internal/adapters/secondary/{whatsmeow,sqlitestore,memory,slogaudit}` |
| `placeholders` | list[path] | Each directory above contains a `.gitkeep` file so empty dirs survive git |
| `governance_files` | list[path] | `LICENSE`, `README.md`, `SECURITY.md`, `.gitignore`, `.editorconfig`, `CLAUDE.md`, `go.mod` |
| `excluded` | list[path] | The bootstrap explicitly does NOT contain `*.go` source, vendored deps, build artifacts (`/wa`, `/wad`, `/dist/`), session DBs, secrets — see `.gitignore` |

**Validation**: spec SC-005 requires that `find cmd internal -type d` exactly matches the documented tree. Verified at plan time.

---

### 6. Plan Artifact Set

The Phase 1 outputs of `/speckit:plan`.

| Artifact | Path | Status |
|---|---|---|
| Plan | `specs/001-research-bootstrap/plan.md` | this commit |
| Data model | `specs/001-research-bootstrap/data-model.md` | this commit |
| Research | `specs/001-research-bootstrap/research.md` | exists from `/speckit:specify` |
| Quickstart | `specs/001-research-bootstrap/quickstart.md` | this commit |
| Contracts | `specs/001-research-bootstrap/contracts/{research-md,repository-state,scaffold-tree}.md` | this commit |
| Tasks | `specs/001-research-bootstrap/tasks.md` | NOT created here (next: `/speckit:tasks`) |

## Relationships and invariants

- **Open Question 1..1 Resolution**: every question has exactly one resolution row in research.md.
- **Resolution 1..N Dossier**: a resolution may aggregate evidence from multiple dossiers; it may also have zero dossier inputs if the main session re-derived everything (Channels API is the prime example).
- **Repository 1..1 Scaffold**: the scaffold lives inside one repository; this feature does not produce mirrors or forks.
- **Scaffold MUST NOT contain executable code**: enforced by spec FR-015. The `go.mod` file is allowed because it is a manifest, not source.
- **Resolution.evidence_urls MUST be non-empty when status = resolved**: enforced by spec FR-002 and CHK027.

## What this data model is NOT

It is not the data model for the future Go code. There is no `JID` here, no `Contact`, no `Message`, no `Allowlist`. Those entities are listed in [`CLAUDE.md`](../../CLAUDE.md) §"Domain types" and will be formally specified — with field types, validation rules, and Go signatures — in feature 002's own `data-model.md`. Mixing the two would muddle the layer separation hexagonal architecture is meant to enforce.
