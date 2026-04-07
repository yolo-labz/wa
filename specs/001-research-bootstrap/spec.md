# Feature Specification: Research and Bootstrap the wa CLI Project

**Feature Branch**: `001-research-bootstrap`
**Created**: 2026-04-06
**Status**: Draft
**Input**: User description: "Deploy a swarm of agents to conduct extensive research on articles, research and all that jazz in order to answer the open architectural questions in the best way possible. The goal is to start the setup for this project. Compile all the research into a research.md file for the next speckit steps. The goal of this spec is to conduct the entire research and start up the code. Create a GH repo under the yolo-labz org."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open Questions Have Defensible Answers (Priority: P1)

The maintainer needs every "OPEN" question listed in `CLAUDE.md` (pairing default, repo visibility, module path, Channels API specifics, integration-test approach, license, distribution pipeline, governance toolchain, source patterns to copy from existing whatsmeow consumers) answered with cited evidence from current 2025–2026 documentation, real production source code, and authoritative third-party references — not from intuition.

**Why this priority**: Every downstream phase (`speckit:plan`, `speckit:tasks`, the first commit of Go code) depends on these answers. An unanswered question becomes a placeholder that infects later artifacts; an answered-but-unsourced question becomes a footgun the next time WhatsApp ships a protocol change.

**Independent Test**: Open `specs/001-research-bootstrap/research.md`. For each open question listed in `CLAUDE.md`, verify there is a section with (a) the answer, (b) at least one cited URL, (c) the rationale connecting evidence to recommendation. Spot-check three citations by visiting the URLs.

**Acceptance Scenarios**:

1. **Given** the OPEN questions list in CLAUDE.md, **When** a reader opens `research.md`, **Then** every question appears as a heading with a defended answer and at least one source link.
2. **Given** a future maintainer who disagrees with a recommendation, **When** they read the rationale and citations, **Then** they can locate the original source within one click and audit the reasoning.
3. **Given** the Channels API uncertainty flagged by an earlier research pass, **When** the maintainer reads `research.md`, **Then** the actual transport, event schema, and Telegram-plugin filenames are documented as confirmed against live Anthropic docs (or explicitly flagged as still uncertain with the exact URL that needs to be re-fetched).

---

### User Story 2 - Project Repository Exists and Is Ready to Receive Code (Priority: P1)

The maintainer needs a remote GitHub repository at `github.com/yolo-labz/wa` with the initial blueprint, spec, research, and a working speckit feature branch already pushed, so that the next phase (`speckit:plan` → `speckit:tasks` → `speckit:implement`) writes code into a real git history rather than into a vacuum.

**Why this priority**: The user's stated goal is "start up the code." Code without a remote is throwaway; the remote must exist before the first Go file is written so that every commit lands somewhere durable.

**Independent Test**: `gh repo view yolo-labz/wa` returns a populated repository description. The default branch contains `CLAUDE.md`, `.specify/`, and `specs/001-research-bootstrap/{spec.md,research.md,checklists/requirements.md}`. The `001-research-bootstrap` branch is pushed and visible on GitHub.

**Acceptance Scenarios**:

1. **Given** the maintainer has a fresh machine, **When** they `git clone https://github.com/yolo-labz/wa`, **Then** they get the full blueprint and the active feature branch.
2. **Given** the repository, **When** the maintainer runs `gh pr list`, **Then** they see no PRs yet (the feature branch is the working branch, not a PR).
3. **Given** the repository, **When** another contributor opens it, **Then** the README or CLAUDE.md immediately tells them what the project is, what state it is in, and where to find the spec.

---

### User Story 3 - Repository Is Scaffolded for the First Code Commit (Priority: P2)

The maintainer needs the repository to contain the directory skeleton (`cmd/wa/`, `cmd/wad/`, `internal/{domain,app,adapters/{primary,secondary}}/`), a `go.mod` declaring `github.com/yolo-labz/wa` with the right Go version, a license file, a `.gitignore` for Go, a basic `README.md`, and an empty governance file set so the very next session can `go build` without first having to argue about layout.

**Why this priority**: Empty `internal/` directories cost nothing to create now and remove a friction point from the next session. Putting them off means the first code commit also has to settle layout debates a second time.

**Independent Test**: `find . -type d -name internal` returns the four expected adapter directories; `go mod tidy` succeeds (even with zero `.go` files); `cat LICENSE` returns a valid SPDX-recognized license; `cat .gitignore` includes Go build artifacts.

**Acceptance Scenarios**:

1. **Given** the scaffolded repo, **When** a Go developer runs `go mod tidy && go vet ./...`, **Then** both succeed without errors.
2. **Given** the scaffolded repo, **When** the developer reads the directory tree, **Then** the placement of every future file is unambiguous because the hexagonal layout from CLAUDE.md is already physically present.
3. **Given** the scaffolded repo, **When** they look for governance files, **Then** `LICENSE`, `.gitignore`, `.editorconfig`, and `README.md` exist with non-placeholder contents.

---

### Edge Cases

- **What happens if a research agent's web fetch fails for the Channels API page?** The dossier must explicitly mark that fact as "UNVERIFIED — re-verify URL" rather than fabricate field names. The maintainer should be able to find the unverified items in `research.md` by searching for "UNVERIFIED".
- **What happens if `yolo-labz/wa` already exists on GitHub?** The bootstrap step must detect this and reuse the existing repo (push the new branch) rather than fail or create `wa-2`.
- **What happens if a research recommendation contradicts a decision already locked in CLAUDE.md?** The contradiction must be surfaced explicitly in `research.md` under a "Contradicts blueprint" callout, never silently overwrite the blueprint.
- **What happens if `gh auth status` reveals the active token lacks the scope needed to create a repo under `yolo-labz`?** The bootstrap step must fail loudly and tell the maintainer the exact `gh auth refresh -s …` command to run.
- **What happens to integration tests without a burner WhatsApp number?** They are gated behind an environment flag (`WA_INTEGRATION=1`) and skipped by default — the spec must record this as the v0 default rather than leaving it ambiguous.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The deliverables of this feature MUST include `specs/001-research-bootstrap/research.md` containing at least one cited answer for every OPEN question listed in `CLAUDE.md`.
- **FR-002**: Every recommendation in `research.md` MUST link to a primary source (official documentation, upstream repository, peer-reviewed reference, or the source code of a named production consumer).
- **FR-003**: The Channels API integration plan in `research.md` MUST be either confirmed against live Anthropic documentation or explicitly marked as `UNVERIFIED — fetch <URL>` so the next session knows what to re-check.
- **FR-004**: The remote repository `github.com/yolo-labz/wa` MUST exist after this feature is complete, MUST be public, and MUST contain the `main` branch and the `001-research-bootstrap` branch.
- **FR-005**: The repository MUST contain the hexagonal directory skeleton (`cmd/wa`, `cmd/wad`, `internal/domain`, `internal/app`, `internal/adapters/primary`, `internal/adapters/secondary`) with `.gitkeep` placeholders so future commits do not have to create these directories.
- **FR-006**: The repository MUST contain a `go.mod` declaring `github.com/yolo-labz/wa` with the chosen Go toolchain version recorded.
- **FR-007**: The repository MUST contain a top-level `LICENSE` file matching the license recommendation in `research.md`.
- **FR-008**: The repository MUST contain a `.gitignore` covering Go build artifacts, editor swap files, and OS metadata files.
- **FR-009**: The repository MUST contain a `README.md` that, in under 200 lines, tells a new reader what the project is, points them at `CLAUDE.md` for architecture and at `specs/001-research-bootstrap/` for the active spec, and lists the install/build prerequisites.
- **FR-010**: The repository MUST contain a `SECURITY.md` with at minimum a threat model section enumerating: prompt injection from inbound messages, malicious allowlisted contact, lost laptop, supply-chain compromise of whatsmeow upstream.
- **FR-011**: The research process MUST run multiple agents in parallel against non-overlapping question scopes; sequential research is prohibited because it does not scale to the question count.
- **FR-012**: Every locked decision in CLAUDE.md MUST remain locked unless `research.md` produces direct evidence to the contrary, in which case the contradiction is recorded explicitly and escalated to the maintainer rather than silently changed.
- **FR-013**: The feature is complete only when (a) every checklist item under `specs/001-research-bootstrap/checklists/requirements.md` passes, (b) `git status` is clean, (c) the branch is pushed to `origin/001-research-bootstrap`.
- **FR-014**: A `requirements.md` checklist file MUST be generated alongside the spec under `checklists/` and MUST be re-runnable after each iteration of the spec.
- **FR-015**: The bootstrap MUST NOT include the `wa` Go binary itself, generated builds, or vendored third-party source — only the directory skeleton, governance files, the spec, and the research.

### Key Entities

- **Open Question**: A named architectural decision that the prior CLAUDE.md blueprint deferred. Each has a unique label, a precise wording, a current default (if any), and at minimum one acceptable answer.
- **Dossier**: The output of a single research agent. Each dossier is scoped to a non-overlapping topic (Channels API, pairing UX, distribution pipeline, governance toolchain, source patterns from prior art).
- **research.md**: The synthesized union of all dossiers, organized by Open Question, with deduplication, cross-references back to CLAUDE.md, and an explicit "Contradicts blueprint" section if any.
- **Repository**: The remote GitHub project under `yolo-labz/wa` that holds the blueprint, spec, research, scaffold, and (later) source code.
- **Scaffold**: The set of directories and governance files that exist in the repository before the first Go source file is written. Includes `cmd/`, `internal/`, `LICENSE`, `.gitignore`, `README.md`, `SECURITY.md`, `go.mod`.
- **OPEN Question Resolution**: A row in `research.md` consisting of (question, recommended answer, evidence URLs, rationale, status: `resolved` or `unverified`).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100 % of the OPEN questions enumerated in `CLAUDE.md` have a corresponding answered section in `research.md`. Verifiable by counting headings.
- **SC-002**: Every recommendation in `research.md` carries at least one inline citation; zero recommendations are unsourced. Verifiable by grepping each section for an `http(s)://` URL.
- **SC-003**: A new collaborator can clone `yolo-labz/wa`, read `README.md` + `CLAUDE.md` + `specs/001-research-bootstrap/spec.md` + `research.md`, and produce a credible one-paragraph summary of the project goals and current state in under 15 minutes.
- **SC-004**: The bootstrap step completes without manual intervention beyond responding to `gh` authentication prompts: a single shell session can take an empty directory to "feature branch pushed to GitHub with spec and research."
- **SC-005**: The hexagonal directory skeleton matches CLAUDE.md exactly: a diff between the documented tree and `find cmd internal -type d` returns zero structural differences.
- **SC-006**: At least five independent research agents ran in parallel; no agent's scope overlapped another's by more than 10 % of cited URLs.
- **SC-007**: Zero items in the requirements checklist remain `[ ]` (unchecked) at feature close. If any item cannot be checked, the reason is documented in the checklist Notes section.

## Assumptions

- The maintainer's `gh` CLI is authenticated against `github.com/phsb5321` with at least `repo` scope, and `phsb5321` is a member of the `yolo-labz` organization with permission to create public repositories. (Confirmed during this session: `gh auth status` returned the `repo` scope and `gh api orgs/yolo-labz` returned HTTP 200.)
- The repository visibility default is **public** for v0. The user explicitly chose to host under an organization with only public repositories so far; this can be flipped to private with `gh repo edit --visibility private` later if any safety concern emerges.
- The Go module path is `github.com/yolo-labz/wa`. The binary names are `wa` (CLI client) and `wad` (daemon), per CLAUDE.md.
- No burner WhatsApp number is available in this session. Integration tests against the live WhatsApp protocol are gated behind `WA_INTEGRATION=1` and excluded from CI by default.
- Research agents will use the deep-researcher subagent to fetch live documentation via WebFetch; results are cited inline. If a fetch fails, the agent must mark the fact as `UNVERIFIED` rather than guess.
- This spec describes only the research and bootstrap step. Writing the actual `*.go` source files for `cmd/wa`, `cmd/wad`, the domain types, the ports, and the whatsmeow adapter is the scope of a later feature (likely `002-domain-and-ports` and `003-whatsmeow-adapter`), not this one.
- The license recommendation default carried over from CLAUDE.md is **MPL-2.0**, matching whatsmeow upstream. Research may revise this; if it does, the LICENSE file in the bootstrap matches the new recommendation.
- The user runs nix-darwin; a `flake.nix` is desired but not strictly blocking for this feature — it can be added in a follow-up commit if research surfaces blockers.
