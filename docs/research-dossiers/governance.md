# Governance dossier — feature 002 prep

**Status**: configurations now active in the repo root (committed under `001-research-bootstrap`). This file is the long-form rationale and reference; the live files are in their canonical locations.
**Origin**: produced by the deep-research swarm during `001-research-bootstrap`. The recommendation is to land all five files before any `.go` source so the depguard rule, conventional-commit hook, and CI workflow exist on day one of feature 002.

## Files

| Live path | Purpose |
|---|---|
| `.golangci.yml` | linter configuration; **enforces the hexagonal core/adapter boundary via `depguard`** |
| `cliff.toml` | `git-cliff` config: conventional commits → `CHANGELOG.md` |
| `renovate.json` | dependency bump automation with a special `whatsmeow` package rule |
| `lefthook.yml` | pre-commit `gofumpt` + `golangci-lint`, commit-msg conventional-commit regex, pre-push `go test` + `govulncheck` |
| `.github/workflows/ci.yml` | three GH Actions jobs: `lint`, `test`, `govulncheck` |

## License decision (resolved)

**Apache-2.0**, not MPL-2.0. The MPL-2.0 default in the original CLAUDE.md was overturned during feature 001 research. Rationale, with sources:

- **Mozilla MPL-2.0 FAQ Q9–Q11**: linking to MPL code from differently-licensed code is permitted; only MPL-licensed *files themselves* must remain MPL. So consuming whatsmeow does not force this project to be MPL. <https://www.mozilla.org/en-US/MPL/2.0/FAQ/>
- **Apache-2.0 patent grant**: gives an explicit patent peace clause that MIT lacks. <https://choosealicense.com/licenses/apache-2.0/>
- **Telegram channel plugin precedent**: the official `anthropics/claude-plugins-official/external_plugins/telegram/package.json` declares `"license": "Apache-2.0"`. This is the closest precedent we have for "what license should a Claude Code plugin and its companion CLI use."
- **AGPL-3.0 rejected**: triggers on network interaction with users, which a local CLI does not do; only adds corp-legal friction with no reciprocal benefit.

## Conventional commits + changelog

Tooling: **`git-cliff`** (Rust), not `release-please`.

| | git-cliff | release-please |
|---|---|---|
| Local, deterministic | yes | no, GH-app dependent |
| Works in Nix devShell | yes | partial |
| Go monorepo monolith fit | excellent | better for multi-package orgs |
| Maintenance bot | none required | yes (release PR) |

For a single-binary single-maintainer project, `git-cliff` wins. Live config in `cliff.toml`. Run `git cliff -o CHANGELOG.md` after any release tag.

## golangci-lint with depguard

The `depguard` rule `core-no-whatsmeow` is the **single most important architectural invariant in the repo**. It forbids importing `go.mau.fi/whatsmeow` (or any subpackage) from any file under `internal/domain/**` or `internal/app/**`. The hexagonal core stays library-agnostic by force, not by convention.

Other linters enabled: `errcheck`, `forbidigo` (bans `panic` outside `main`, `fmt.Print*` outside `cmd/`), `gocritic`, `gocyclo`, `gofumpt`, `gosec`, `govet`, `revive`, `staticcheck`. Test files are exempted from `gosec`/`gocyclo`/`forbidigo`.

## Renovate (not Dependabot)

Renovate wins specifically because of how it presents Go pseudo-version bumps (whatsmeow has no semver tags). Special `whatsmeow` rule:

- `schedule: "at any time"` (not weekly — protocol breakage is urgent)
- `semanticCommitType: "fix"` (so it gets surfaced in changelogs)
- `fetchChangeLogs: branch` (renders the upstream commit range in the PR body)
- `prBodyNotes`: "Pseudo-version bump. Review upstream commits before merging."

All other Go modules are grouped weekly to reduce noise.

## govulncheck in CI

A separate GH Actions job, not gating PR merges by default — a fresh CVE in a transitive dep should not block unrelated PRs. The job runs on every push and the failure is informational; tighten to blocking on `main` only when the team grows beyond one person.

## Pre-commit (lefthook)

`lefthook` is a single Go binary, beats `pre-commit` (Python) for a Nix devShell. Three layers:

1. **pre-commit**: `gofumpt` formatting, `golangci-lint --new-from-rev=HEAD~1` (only on changed code)
2. **commit-msg**: regex `^(feat|fix|refactor|chore|docs|test|perf|build|ci)(\(.+\))?!?: .+`
3. **pre-push**: `go test ./...`, `govulncheck ./...`

## Sources

- <https://golangci-lint.run/usage/linters/#depguard>
- <https://github.com/orhun/git-cliff>
- <https://docs.renovatebot.com/modules/manager/gomod/>
- <https://github.com/golang/govulncheck-action>
- <https://github.com/evilmartians/lefthook>
- <https://www.mozilla.org/en-US/MPL/2.0/FAQ/>
- <https://choosealicense.com/licenses/apache-2.0/>
