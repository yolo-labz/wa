# Quickstart: Release Packaging and Service Integration

**Feature**: 007-release-packaging
**Goal**: Validate all packaging artifacts from a fresh clone in under 5 minutes.

## 0. Prerequisites

```bash
go version           # 1.25+
goreleaser --version # v2+
nix --version        # 2.18+ with flakes enabled
git-cliff --version  # 2.0+
```

## 1. Validate GoReleaser config

```bash
goreleaser check
```

Expected: "config is valid"

## 2. Snapshot release (no publish)

```bash
goreleaser release --snapshot --clean
```

Expected: `dist/` contains 3 tarballs (darwin-arm64, linux-amd64, linux-arm64) + checksums.txt. Each tarball contains `wa` and `wad` binaries.

## 3. Build via Nix

```bash
nix build .#default
ls result/bin/
```

Expected: `wa` and `wad` in `result/bin/`. Both are statically linked (`file result/bin/wa` shows "statically linked" or no dynamic section).

## 4. Nix flake check

```bash
nix flake check
```

Expected: all checks pass (build + tests).

## 5. Service dry-run (macOS)

```bash
go run ./cmd/wad install-service --dry-run
```

Expected: valid plist XML printed to stdout. Validate: pipe through `plutil -lint -`.

## 6. Service dry-run (Linux)

```bash
GOOS=linux go run ./cmd/wad install-service --dry-run
```

Expected: valid systemd unit file printed to stdout.

## 7. Upgrade detection

```bash
go run ./cmd/wa upgrade
```

Expected: prints a GitHub Releases URL (since the binary was built via `go run`, not brew/nix).

## 8. CHANGELOG generation

```bash
git-cliff --unreleased
```

Expected: markdown with grouped conventional commit entries.

## 9. Full test suite

```bash
go test -race ./...
golangci-lint run ./...
```

Expected: all green. No regressions from features 001-006.

## 10. Tag and release (manual, maintainer only)

```bash
git tag -a v0.1.0 -m "release: v0.1.0"
git push origin v0.1.0
# GitHub Actions release workflow triggers automatically
```

---

## What this quickstart does NOT cover

- Actually running rcodesign (requires Apple certificates)
- Publishing to Homebrew tap (requires HOMEBREW_TAP_GITHUB_TOKEN)
- systemctl/launchctl operations (requires installed binaries, not go run)
