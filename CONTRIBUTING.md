# Contributing to wa

## Branching model (trunk-based + speckit features)

- `main` is always releasable. Direct pushes are blocked; everything lands via PR.
- Feature work happens on `NNN-short-name` branches produced by `/speckit:specify`
  (e.g. `003-whatsmeow-adapter`). One branch per spec, squash- or merge-commit
  into `main` once CI is green and the spec's tasks are all `[X]`.
- Hotfixes: `fix/<short-desc>` branches off `main`, fast-tracked PR.
- Releases are tagged `vMAJOR.MINOR.PATCH[-<feature>]` from `main`.

## Conventional commits

Every commit subject MUST match:

```
<type>(<scope>)?!?: <description>
```

`type` ∈ `feat fix docs style refactor perf test build ci chore revert`.
Subject ≤72 chars. Breaking changes use `!` and a `BREAKING CHANGE:` footer.

Examples:
- `feat(adapter/whatsmeow): add pairing flow`
- `fix(domain): reject empty JID user segment`
- `chore(ci): bump golangci-lint to v2`

The commit-msg hook (`scripts/commit-msg-check.sh`, wired by lefthook) and the
`commitlint` job in `.github/workflows/ci.yml` enforce this on every PR.

## Local setup

```bash
# 1. install lefthook (one-time)
brew install lefthook        # or: go install github.com/evilmartians/lefthook@latest

# 2. install hooks into .git/hooks
lefthook install

# 3. install golangci-lint
brew install golangci-lint
```

The hooks run:

| Stage      | Command                                                          |
|------------|------------------------------------------------------------------|
| pre-commit | `gofmt -l`, `go vet ./...`, `golangci-lint run`, `go mod tidy`  |
| commit-msg | `scripts/commit-msg-check.sh`                                   |
| pre-push   | `go test -race -count=1 ./...`                                  |

## Running tests

```bash
# Full test suite with race detection and shuffle
go test -race -shuffle=on -count=1 ./...

# Single package
go test -race -shuffle=on -count=1 ./internal/domain/...

# With coverage
go test -race -covermode=atomic -coverprofile=cover.out ./...
go tool cover -func=cover.out
```

## Running fuzz tests

```bash
# Run a specific fuzz target for 30 seconds
go test -fuzz=FuzzParse -fuzztime=30s ./internal/domain/

# Run all fuzz targets
go test -fuzz=. -fuzztime=10s ./internal/domain/
```

## Test coverage

- Local: `go test -race -covermode=atomic -coverprofile=cover.out ./... && go tool cover -func=cover.out`
- CI uploads `cover.out` to SonarQube on every push to `main` and every PR.
- Target: ≥80 % on `internal/domain` and `internal/app`. Adapters are exempted —
  contract suites in `porttest/` are the source of truth there.

## CI checks

CI runs the following on every PR:

1. **lint** — golangci-lint with depguard
2. **test** — `go test -race` with coverage
3. **sonar** — SonarQube analysis
4. **OSV-Scanner** — vulnerability scanning (replaces govulncheck, runs call-graph analysis internally)
5. **CodeQL** — static analysis for Go + Actions
6. **commitlint** — PR title validation
7. **nix** — flake check + build

## Pull request checklist

See `.github/PULL_REQUEST_TEMPLATE.md`. PRs require:

1. CI green (lint, test, OSV-Scanner, commitlint, CodeQL).
2. One CODEOWNERS approval.
3. Branch up to date with `main`.
4. No force-push after review starts; use new commits, squash on merge.

## Releasing

1. Merge feature PR into `main`.
2. `git tag -a vX.Y.Z -m "release notes"` from `main`.
3. `git push origin vX.Y.Z`.
4. GoReleaser builds binaries, generates SBOMs, signs with cosign keyless,
   and attaches provenance attestations to the GitHub Release.
