# Data Model: Release Packaging and Service Integration

**Feature**: 007-release-packaging
**Date**: 2026-04-10

This feature has no runtime data model — it's configuration files and CLI subcommands. This document catalogs the deliverables and their relationships.

## Artifact inventory

| Artifact | Type | Lives in repo | Generated at |
|---|---|---|---|
| `.goreleaser.yaml` | YAML config | Yes | Authored once |
| `.github/workflows/release.yml` | YAML workflow | Yes | Authored once |
| `flake.nix` | Nix expression | Yes | Authored once |
| `flake.lock` | JSON lockfile | Yes | `nix flake update` |
| launchd plist | XML | No (generated) | `wad install-service` on macOS |
| systemd unit | INI | No (generated) | `wad install-service` on Linux |
| `CHANGELOG.md` | Markdown | Yes | `git-cliff` on release |
| Homebrew formula | Ruby | No (in tap repo) | GoReleaser on release |

## GoReleaser build matrix

| ID | Binary | GOOS | GOARCH | Notarized |
|---|---|---|---|---|
| wa-darwin-arm64 | wa | darwin | arm64 | Yes |
| wad-darwin-arm64 | wad | darwin | arm64 | Yes |
| wa-linux-amd64 | wa | linux | amd64 | No |
| wad-linux-amd64 | wad | linux | amd64 | No |
| wa-linux-arm64 | wa | linux | arm64 | No |
| wad-linux-arm64 | wad | linux | arm64 | No |

6 binaries total, packaged into 3 tarballs (one per GOOS-GOARCH pair).

## Release workflow sequence

```
1. Maintainer pushes vX.Y.Z tag to main
2. .github/workflows/release.yml triggers
3. goreleaser release:
   a. Cross-compile 6 binaries
   b. Post-build hook: rcodesign sign darwin binaries
   c. Archive into 3 tarballs + checksums.txt
   d. Post-archive hook: rcodesign notary-submit darwin tarball contents
   e. Upload to GitHub Releases
   f. Update Homebrew tap formula
4. git-cliff generates CHANGELOG.md
5. Commit CHANGELOG.md to main
```

## Install-method detection (wa upgrade)

```
Binary path contains "/Cellar/" or "/homebrew/" → Homebrew
Binary path contains "/nix/store/" → Nix
main.version contains "devel" or "go install" → go install
Fallback → GitHub Releases URL
```

## LOC budget

| Component | Estimate |
|---|---|
| `.goreleaser.yaml` | 100 |
| `.github/workflows/release.yml` | 80 |
| `flake.nix` | 60 |
| `service.go` + `service_darwin.go` + `service_linux.go` | 150 |
| `service_test.go` | 40 |
| `cmd_upgrade.go` | 40 |
| Modified files | 20 |
| **Total** | **~490** |
