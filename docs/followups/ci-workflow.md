# Follow-up: install `.github/workflows/ci.yml`

The CI workflow file (`lint`, `test`, `govulncheck` jobs against the `wa` Go module) was authored during the "solve everything" pass but could not be pushed in the same session because the active `gh` OAuth token lacks the `workflow` scope. GitHub rejected the push with:

```
! [remote rejected] 001-research-bootstrap -> 001-research-bootstrap
  (refusing to allow an OAuth App to create or update workflow
   .github/workflows/ci.yml without `workflow` scope)
```

## Action required from the maintainer

```sh
gh auth refresh -h github.com -s workflow
```

This opens a browser flow to grant the `workflow` scope to the existing token. After it succeeds, the literal CI workflow body is recorded below — paste it into `.github/workflows/ci.yml`, commit, and push.

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "stable"
          cache: true
      - uses: golangci/golangci-lint-action@v6
        with:
          version: v1.62

  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "stable"
          cache: true
      - run: go test -race -covermode=atomic -coverprofile=cover.out ./...

  govulncheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: golang/govulncheck-action@v1
        with:
          go-version-input: "stable"
          check-latest: true
```

The workflow has zero untrusted-input fields (no `${{ github.event.* }}` references), so the GitHub Actions injection guidance does not apply. It is safe to land as-is.

## Why it was authored even though it cannot run

`golangci-lint` will report no findings against an empty Go module — that is fine. `go test ./...` will report `no test files` — also fine. `govulncheck` will report nothing. The workflow exists so that the very first `.go` file landing under `internal/domain/` in feature 002 immediately gets lint, test, and vulnerability checks without a separate "set up CI" PR.
