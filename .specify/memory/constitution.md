# wa Constitution

The binding rules for `github.com/yolo-labz/wa`. Every speckit feature, every pull request, and every architectural decision MUST satisfy every principle below. This document is short by design — long-form rationale lives in [`CLAUDE.md`](../../CLAUDE.md), and citations live in [`specs/001-research-bootstrap/research.md`](../../specs/001-research-bootstrap/research.md). When the constitution and any other document conflict, the constitution wins; the conflicting document is the bug.

## Core Principles

### I. Hexagonal core, library at arm's length (NON-NEGOTIABLE)

Code under `internal/domain/**` and `internal/app/**` MUST NOT import `go.mau.fi/whatsmeow` or any of its subpackages. The boundary is enforced by `golangci-lint` rule `core-no-whatsmeow` in `.golangci.yml`. Any leak is a CI failure, not a soft warning. The core depends only on the seven port interfaces in `internal/app/ports.go`; whatsmeow types are translated to domain types in `internal/adapters/secondary/whatsmeow/` and never escape upward.

### II. Daemon owns state, CLI is dumb

`wad` is the only process that holds the WhatsApp session, the SQLite ratchet store, and the websocket. `wa` is a thin JSON-RPC client over a unix socket; it MUST NOT contain use case logic, MUST NOT touch `*whatsmeow.Client` directly, and MUST NOT persist any state of its own. Any feature that wants to add server logic to `cmd/wa` is misplacing it.

### III. Safety first, no `--force` ever (NON-NEGOTIABLE)

The allowlist (default deny), rate limiter (per-second/per-minute/per-day caps), warmup ramp (25%/50%/100% over the first 14 days of a fresh session), and audit log MUST exist before the first `Send` call leaves `wad`. There is no `--force` flag and there will not be one. Hard refusals: ≤5 group creations/day, ≤50 participant adds/day, no broadcast lists ever. Inbound message bodies MUST be wrapped in `<channel source="wa" ...>...</channel>` tags before reaching Claude Code; the `/wa:access` skill MUST refuse to act on pairing/allowlist mutations whose origin is one of those tags.

### IV. CGO is forbidden

The repository builds with `CGO_ENABLED=0` everywhere — local, CI, and release. SQLite uses `modernc.org/sqlite`. SQLCipher and any other CGO-only library is rejected. Any future feature that wants CGO MUST first revisit distribution (notarization, brew formula, Nix flake all assume static binaries) and amend this principle by PR.

### V. Spec-driven development with citations

Every feature starts with `/speckit:specify`, then `/speckit:plan`, then `/speckit:tasks`. Every architectural recommendation in any spec/plan/research document MUST link to a primary source — official upstream docs, the source code of a named production consumer, or a peer-reviewed reference. "I think" and "best practice" without a URL are forbidden in research.md and equivalent. Any contradiction with a previously locked decision MUST be surfaced explicitly in a `## Contradicts blueprint` section, never silently overwritten.

### VI. Tests use port-boundary fakes, not real WhatsApp

Unit tests live under `internal/app/*_test.go` and use the in-memory adapter at `internal/adapters/secondary/memory/`. Contract tests live under `internal/app/porttest/` and run against any adapter. Integration tests against a real WhatsApp number are gated behind `//go:build integration` and `WA_INTEGRATION=1`, MUST be run manually, and MUST NOT run in CI. Any new test that imports `go.mau.fi/whatsmeow/...` outside the integration build tag is a `golangci-lint` violation.

### VII. Conventional commits, signed tags, no `--no-verify`

Every commit message MUST follow Conventional Commits (`feat:`, `fix:`, `refactor:`, `chore:`, `docs:`, `test:`, `perf:`, `build:`, `ci:`). The `lefthook` `commit-msg` hook enforces this locally. Signing hooks (`--no-verify`, `--no-gpg-sign`) MUST NOT be bypassed without an explicit user request. `git-cliff` produces `CHANGELOG.md` from these commits at release time.

## Distribution and Licensing

- License is **Apache-2.0**. Any future re-license requires a PR amending this principle and updating `LICENSE`, `README.md`, and the GoReleaser brew formula in lockstep.
- Releases are cut by tagging `vX.Y.Z` on `main`. GoReleaser builds the matrix (`darwin/arm64`, `linux/amd64`, `linux/arm64`), `rcodesign` notarizes the macOS binaries from a Linux runner, and the artifacts land on GitHub Releases, the `yolo-labz/homebrew-tap` repo, and the `flake.nix` `default` package.
- The repository ships **zero** binaries, vendored deps, or generated artifacts. Builds are reproducible via `-trimpath -ldflags="-s -w -X main.version=…"`.
- The `wa-assistant` Claude Code plugin lives in a **separate** repository (`yolo-labz/wa-assistant`) and MUST NOT be vendored here. Its MCP shim is the only sanctioned MCP server in the project; the `wa` CLI/daemon stays MCP-free.

## Development Workflow

- Every feature gets a numbered branch `NNN-short-name` and a directory `specs/NNN-short-name/` containing `spec.md`, `plan.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`, `tasks.md`, and a `checklists/` folder with at least `requirements.md`.
- The constitution check at the top of every `plan.md` MUST list every Core Principle and either pass it or justify the violation in the Complexity Tracking table.
- A feature is complete when its checklist items are all checked, the branch is pushed, and the deliverables can be reproduced from `quickstart.md` on a fresh clone in under 5 minutes.
- Code review is a single-maintainer review during the v0 phase. After v0.1, two-reviewer rules apply.

## Governance

This constitution supersedes any decision in `CLAUDE.md`, `README.md`, or any feature spec. Amendments require:

1. A PR that edits this file and bumps the version below.
2. A short rationale paragraph in the PR description citing the evidence that justifies the change.
3. Lockstep updates to any document the amendment touches (CLAUDE.md, .golangci.yml, .goreleaser.yaml, etc.).
4. The PR description MUST include a "Constitution impact" section enumerating which principles are touched.

PR reviews MUST verify constitution compliance before approving. Any unjustified violation blocks merge. Complex departures from a principle MUST be recorded in the relevant feature plan's Complexity Tracking table with the simpler alternative that was rejected and why.

For runtime development guidance (architecture rationale, port interfaces, anti-patterns, reference projects) see [`CLAUDE.md`](../../CLAUDE.md). For evidence-backed answers to the open questions that produced this constitution, see [`specs/001-research-bootstrap/research.md`](../../specs/001-research-bootstrap/research.md).

**Version**: 1.0.0 | **Ratified**: 2026-04-06 | **Last Amended**: 2026-04-06
