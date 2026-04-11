# Feature Specification: Release Packaging and Service Integration

**Feature Branch**: `007-release-packaging`
**Created**: 2026-04-10
**Status**: Draft
**Input**: User description: "Feature 007: Release packaging and service integration. GoReleaser v2, macOS notarization via rcodesign, Homebrew tap, Nix flake, wad install-service (launchd/systemd), GitHub Actions release workflow, CHANGELOG via git-cliff, wa upgrade prints brew/nix command."

## Overview

This is the final feature before v0.1.0. Features 001-006 delivered a functionally complete daemon (`wad`) and CLI (`wa`) — pair, send, allowlist, safety pipeline, graceful shutdown all work. Feature 007 makes those binaries **distributable** and **installable as a system service**. It adds no new business logic; it packages what exists into release artifacts that users can install via `brew install yolo-labz/tap/wa`, `nix profile install github:yolo-labz/wa`, or by downloading a tarball from GitHub Releases.

The three pillars are:
1. **Build pipeline**: GoReleaser v2 cross-compiles for 3 targets (darwin-arm64, linux-amd64, linux-arm64), injects version via `-ldflags`, notarizes the macOS binary via `rcodesign` from a Linux CI runner, and publishes to GitHub Releases.
2. **Package managers**: A Homebrew tap formula at `yolo-labz/homebrew-tap` and a Nix flake in the repo root provide `brew install` and `nix profile install` paths.
3. **Service integration**: `wad install-service` generates a launchd user agent plist (macOS) or a systemd user unit (Linux) so the daemon starts on login and restarts on failure. Never root.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Install via Homebrew and run as a service (Priority: P1)

As a macOS user, I run `brew install yolo-labz/tap/wa` which installs both `wa` and `wad` binaries. I then run `wad install-service` which generates a launchd plist at `~/Library/LaunchAgents/com.yolo-labz.wad.plist` and loads it with `launchctl load`. The daemon starts automatically on login from now on. If it crashes, launchd restarts it. I can stop it with `launchctl unload` or `wad uninstall-service`.

**Why this priority**: This is the primary distribution channel for macOS users. Without it, users must build from source.

**Independent Test**: The GoReleaser config can be validated locally with `goreleaser check` and `goreleaser release --snapshot --clean` (no publish). The plist generation can be tested by running `wad install-service --dry-run` which prints the plist to stdout without writing it.

**Acceptance Scenarios**:

1. **Given** the Homebrew tap is configured, **When** the user runs `brew install yolo-labz/tap/wa`, **Then** both `wa` and `wad` are installed and available on `$PATH`.
2. **Given** `wad` is installed, **When** the user runs `wad install-service`, **Then** a launchd plist is written to `~/Library/LaunchAgents/com.yolo-labz.wad.plist` and the service is loaded.
3. **Given** the launchd service is loaded, **When** the user logs in, **Then** `wad` starts automatically and the socket is available within 5 seconds.
4. **Given** `wad` crashes, **When** launchd detects the exit, **Then** it restarts `wad` within 10 seconds.
5. **Given** the user wants to stop the service, **When** they run `wad uninstall-service`, **Then** the plist is unloaded and removed.

---

### User Story 2 — Install via Nix and run as a service (Priority: P1)

As a NixOS/nix-darwin user, I run `nix profile install github:yolo-labz/wa` which builds both binaries from the flake. I then run `wad install-service` which generates a systemd user unit (on NixOS) or a launchd plist (on nix-darwin, since the underlying OS is macOS). The Nix flake pins the Go toolchain and produces a reproducible build.

**Why this priority**: The project maintainer uses nix-darwin. This is the dogfooding distribution path.

**Independent Test**: `nix build .#default` in the repo root produces `result/bin/wa` and `result/bin/wad`. `nix flake check` validates the flake.

**Acceptance Scenarios**:

1. **Given** the repo flake, **When** the user runs `nix build .#default`, **Then** `result/bin/wa` and `result/bin/wad` exist and are statically linked (no dynamic libraries).
2. **Given** the flake, **When** the user runs `nix flake check`, **Then** all checks pass (build + tests).
3. **Given** a NixOS system, **When** the user runs `wad install-service`, **Then** a systemd user unit is generated and `systemctl --user enable --now wad` starts the daemon.

---

### User Story 3 — Release pipeline publishes artifacts on tag (Priority: P1)

As the maintainer, when I push a `vX.Y.Z` tag to `main`, a GitHub Actions workflow triggers that: runs `goreleaser release`, cross-compiles for 3 targets, notarizes the macOS binary via `rcodesign`, uploads tarballs + checksums to GitHub Releases, updates the Homebrew tap formula, and generates `CHANGELOG.md` via `git-cliff`. The release is fully automated — I only push the tag.

**Why this priority**: Manual releases are error-prone and don't scale. The pipeline must exist before v0.1.0.

**Independent Test**: `goreleaser release --snapshot --clean` validates the config without publishing. The release workflow can be dry-run tested by pushing a `v0.0.0-test` tag to a fork.

**Acceptance Scenarios**:

1. **Given** a `vX.Y.Z` tag is pushed, **When** the release workflow runs, **Then** GitHub Releases contains tarballs for darwin-arm64, linux-amd64, and linux-arm64, plus a `checksums.txt` file.
2. **Given** the macOS tarball, **When** a user downloads and runs `wad`, **Then** Gatekeeper does not block it (the binary is notarized).
3. **Given** a release, **When** the Homebrew formula is updated, **Then** `brew update && brew upgrade wa` installs the new version.
4. **Given** a release, **When** `CHANGELOG.md` is generated, **Then** it contains conventional-commit-grouped entries since the last tag.

---

### User Story 4 — `wa upgrade` prints the right command (Priority: P2)

As a user who installed via brew or nix, when I run `wa upgrade`, it detects how the binary was installed and prints the correct upgrade command (`brew upgrade yolo-labz/tap/wa` or `nix profile upgrade ...`) rather than attempting an in-process self-update. If the installation method cannot be detected, it prints the GitHub Releases URL.

**Why this priority**: Quality-of-life. The daemon works without it.

**Independent Test**: A unit test mocks the detection logic and asserts the correct command string for each installation method.

**Acceptance Scenarios**:

1. **Given** `wa` was installed via Homebrew, **When** the user runs `wa upgrade`, **Then** stdout prints `brew upgrade yolo-labz/tap/wa`.
2. **Given** `wa` was installed via Nix, **When** the user runs `wa upgrade`, **Then** stdout prints `nix profile upgrade ...`.
3. **Given** `wa` was installed from a tarball, **When** the user runs `wa upgrade`, **Then** stdout prints the GitHub Releases URL.

---

### User Story 5 — CHANGELOG generation (Priority: P2)

As the maintainer, when I prepare a release, `git-cliff` generates a `CHANGELOG.md` from conventional commit messages grouped by type (feat, fix, refactor, etc.) since the last tag. The cliff.toml config is already committed from feature 001; this feature ensures it integrates with the release pipeline.

**Why this priority**: Documentation. The project works without it but releases are opaque.

**Independent Test**: `git-cliff --unreleased` produces markdown output in the expected format.

**Acceptance Scenarios**:

1. **Given** conventional commits since the last tag, **When** `git-cliff` runs, **Then** `CHANGELOG.md` is generated with grouped entries.
2. **Given** the release workflow, **When** a tag is pushed, **Then** `CHANGELOG.md` is committed to the repo as part of the release.

---

### Edge Cases

- **First release (no prior tags)**: GoReleaser and git-cliff must handle the case where there is no previous tag. GoReleaser defaults to all commits since initial; git-cliff generates a single "Unreleased" section.
- **notarization failure**: If `rcodesign` fails (e.g., expired Apple certificate), the release workflow MUST fail visibly and NOT publish un-notarized macOS binaries.
- **Homebrew formula SHA mismatch**: If the tarball SHA in the formula doesn't match the actual release artifact, `brew install` fails with a clear error. The formula generation must compute SHAs from the actual artifacts.
- **`wad install-service` run as root**: MUST refuse with a clear error — the service runs as a user agent, never root.
- **`wad install-service` when service already exists**: MUST update the plist/unit in place (idempotent), not fail.
- **`wad uninstall-service` when service doesn't exist**: MUST succeed silently (idempotent).
- **Nix flake on a system without Nix**: Not applicable — `nix build` requires Nix. The flake is for Nix users only.
- **`wa upgrade` on a `go install`-built binary**: Print `go install github.com/yolo-labz/wa/cmd/wa@latest`.
- **Cross-compilation CGO leak**: GoReleaser MUST set `CGO_ENABLED=0` for all targets, per constitution principle IV.

## Requirements *(mandatory)*

### Functional Requirements

#### GoReleaser configuration

- **FR-001**: `.goreleaser.yaml` MUST define builds for `cmd/wa` and `cmd/wad` targeting darwin-arm64, linux-amd64, and linux-arm64.
- **FR-002**: All builds MUST use `CGO_ENABLED=0`, `-trimpath`, and `-ldflags="-s -w -X main.version={{.Version}}"`.
- **FR-003**: The release MUST produce tarballs (`.tar.gz`) and a `checksums.txt` (SHA256).
- **FR-004**: The macOS darwin-arm64 binary MUST be notarized via `rcodesign sign` using an Apple Developer ID certificate stored as GitHub Actions secrets.
- **FR-005**: `.goreleaser.yaml` MUST include a Homebrew tap section publishing to `yolo-labz/homebrew-tap`.

#### GitHub Actions release workflow

- **FR-006**: `.github/workflows/release.yml` MUST trigger on `v*` tag pushes to `main`.
- **FR-007**: The workflow MUST run `goreleaser release` with the `GITHUB_TOKEN` and any notarization secrets.
- **FR-008**: The workflow MUST fail if notarization fails — un-notarized macOS binaries MUST NOT be published.
- **FR-009**: The workflow MUST run `git-cliff` to generate/update `CHANGELOG.md` and commit it to the repo.

#### Nix flake

- **FR-010**: `flake.nix` MUST define a default package that builds both `wa` and `wad` using `buildGoModule` with the pinned Go version from `go.mod`.
- **FR-011**: `flake.nix` MUST pass `CGO_ENABLED = "0"` and the same ldflags as GoReleaser.
- **FR-012**: `nix flake check` MUST pass (build + `go test ./...`).

#### Service integration

- **FR-013**: `wad install-service` MUST be a subcommand of `wad` (not `wa`) that generates and loads a service definition.
- **FR-014**: On macOS, the service MUST be a launchd user agent plist at `~/Library/LaunchAgents/com.yolo-labz.wad.plist` with `KeepAlive: true`, `RunAtLoad: true`, stdout/stderr redirected to `$XDG_STATE_HOME/wa/wad.log`.
- **FR-015**: On Linux, the service MUST be a systemd user unit at `~/.config/systemd/user/wad.service` with `Restart=on-failure`, `RestartSec=5s`.
- **FR-016**: `wad install-service` MUST refuse to run as root with a clear error.
- **FR-017**: `wad install-service --dry-run` MUST print the generated service file to stdout without writing or loading it.
- **FR-018**: `wad uninstall-service` MUST unload and remove the service file. It MUST be idempotent.
- **FR-019**: The generated service file MUST reference the absolute path to the `wad` binary as installed (not a relative path).

#### `wa upgrade` subcommand

- **FR-020**: `wa upgrade` MUST detect the installation method by checking: (a) if the binary path contains `/Cellar/` → Homebrew; (b) if it contains `/nix/store/` → Nix; (c) if `main.version` contains `go install` metadata → go install; (d) fallback → print GitHub Releases URL.
- **FR-021**: `wa upgrade` MUST print the command to stdout and exit 0 — it MUST NOT execute the command itself.

#### CHANGELOG

- **FR-022**: `cliff.toml` (already committed in feature 001) MUST produce a grouped CHANGELOG.md from conventional commits.
- **FR-023**: The release workflow MUST commit the generated `CHANGELOG.md` to the repo after the release.

### Key Entities

- **`.goreleaser.yaml`** — GoReleaser v2 configuration file. Defines builds, archives, homebrew tap, and notarization.
- **`.github/workflows/release.yml`** — GitHub Actions workflow triggered on tags.
- **`flake.nix`** — Nix flake providing the default package.
- **`cmd/wad/service_darwin.go`** — launchd plist generation (build-tagged).
- **`cmd/wad/service_linux.go`** — systemd unit generation (build-tagged).
- **`cmd/wa/cmd_upgrade.go`** — upgrade command with install-method detection.
- **Homebrew formula** — generated by GoReleaser, published to `yolo-labz/homebrew-tap`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `goreleaser check` validates the GoReleaser config without errors.
- **SC-002**: `goreleaser release --snapshot --clean` produces 3 tarballs + checksums locally.
- **SC-003**: `nix build .#default` produces `result/bin/wa` and `result/bin/wad` that execute correctly.
- **SC-004**: `nix flake check` passes.
- **SC-005**: `wad install-service --dry-run` on macOS produces a valid plist (parseable by `plutil`).
- **SC-006**: `wad install-service --dry-run` on Linux produces a valid systemd unit (parseable by `systemd-analyze verify` or manual inspection).
- **SC-007**: `wa upgrade` prints the correct command for Homebrew, Nix, and fallback paths.
- **SC-008**: The release workflow completes successfully on a `v0.0.0-test` dry-run tag.
- **SC-009**: `CHANGELOG.md` generated by `git-cliff` contains grouped entries for features 001-007.
- **SC-010**: All existing tests continue to pass: `go test -race ./...` green, `golangci-lint run` clean.

## Assumptions

- The Apple Developer ID certificate for notarization is stored as GitHub Actions secrets (`APPLE_CERTIFICATE_BASE64`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_TEAM_ID`). Acquiring the certificate is the maintainer's responsibility and is not automated by this feature.
- The `yolo-labz/homebrew-tap` repository already exists (or will be created manually before the first release). GoReleaser publishes to it via a `HOMEBREW_TAP_GITHUB_TOKEN` secret with repo write access.
- `rcodesign` is installed in the CI runner via a GitHub Action or pre-installed binary. It does not require a macOS runner — it runs on Linux and signs/notarizes remotely.
- The Nix flake assumes the user has Nix installed with flake support enabled (`experimental-features = nix-command flakes`).
- `wad install-service` generates but does NOT start the service. The user must run `launchctl load` or `systemctl --user start wad` separately (or reboot/re-login for `RunAtLoad`/`WantedBy=default.target`).
- `git-cliff` is installed in the release CI runner. The `cliff.toml` from feature 001 is used as-is.

## Dependencies

- **Feature 001 (research-bootstrap)** — `cliff.toml`, `.goreleaser.yaml` skeleton (if any), `renovate.json`.
- **Feature 006 (binaries-wiring)** — `cmd/wad/main.go` and `cmd/wa/main.go` must exist and build.
- External: Apple Developer ID certificate, `yolo-labz/homebrew-tap` repo, GitHub Actions secrets.

## Out of Scope

- In-process self-update (`wa upgrade` only prints the command).
- Windows builds or service integration.
- Docker image publishing.
- `wa doctor` diagnostic subcommand — deferred past v0.1.
- Multi-architecture Nix flake (only the host architecture is built; cross-compilation is via GoReleaser).
- Signing Linux binaries (GPG signing of tarballs is sufficient via GoReleaser checksums).
