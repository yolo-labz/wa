# Spec Quality Checklist: Release Packaging and Service Integration

**Purpose**: Validate spec.md quality for feature 007.
**Created**: 2026-04-10
**Feature**: [spec.md](../spec.md)

## Content Quality

- [X] CHK001 Spec focuses on packaging/distribution behavior, not internal code changes.
- [X] CHK002 Every FR is testable (goreleaser check, nix build, plutil, systemd-analyze, etc.).
- [X] CHK003 Scope boundary explicit: packaging only, no business logic changes.
- [X] CHK004 No vague qualifiers — all SCs have verifiable conditions.

## Requirement Completeness

- [X] CHK005 All 5 user stories have Given/When/Then acceptance scenarios.
- [X] CHK006 GoReleaser targets cover all 3 platforms from CLAUDE.md (darwin-arm64, linux-amd64, linux-arm64).
- [X] CHK007 CGO_ENABLED=0 is specified for both GoReleaser and Nix flake (constitution IV).
- [X] CHK008 Notarization failure behavior is specified: workflow fails, no un-notarized binaries published.
- [X] CHK009 Service integration covers both macOS (launchd) and Linux (systemd) with build tags.
- [X] CHK010 `wad install-service` refuses root and supports `--dry-run`.
- [X] CHK011 `wa upgrade` detection logic covers Homebrew, Nix, go install, and fallback.

## Requirement Clarity

- [X] CHK012 launchd plist path is exact: `~/Library/LaunchAgents/com.yolo-labz.wad.plist`.
- [X] CHK013 systemd unit path is exact: `~/.config/systemd/user/wad.service`.
- [X] CHK014 ldflags format is exact: `-s -w -X main.version={{.Version}}`.
- [X] CHK015 Homebrew tap repo is exact: `yolo-labz/homebrew-tap`.

## Consistency

- [X] CHK016 GoReleaser targets match CLAUDE.md §Distribution exactly.
- [X] CHK017 CGO prohibition matches constitution principle IV.
- [X] CHK018 Exit codes for `wa upgrade` match the exit code table from feature 006.
- [X] CHK019 `cliff.toml` referenced from feature 001 is reused, not recreated.
- [X] CHK020 Service file references absolute binary path per FR-019.

## Edge Cases

- [X] CHK021 First release (no prior tags) handled for both GoReleaser and git-cliff.
- [X] CHK022 Service already exists → idempotent update.
- [X] CHK023 Service doesn't exist on uninstall → silent success.
- [X] CHK024 Notarization secrets missing → clear CI error, not silent failure.

## Notes

- All 24 items pass on first draft.
- The Apple Developer ID certificate acquisition is a manual prerequisite documented in Assumptions.
- This is a config-heavy feature (YAML, Nix, plist, systemd unit) with minimal Go code.
