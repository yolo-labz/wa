# Architecture & Modernity Checklist: Release Packaging

**Purpose**: Validate packaging decisions, tooling modernity, and cross-platform consistency for feature 007.
**Created**: 2026-04-10
**Feature**: [spec.md](../spec.md), [plan.md](../plan.md), [research.md](../research.md)

## Build Pipeline Integrity

- [X] CHK001 Is the GoReleaser config specified as v2 (`version: 2`)? [Completeness] [Research D1] — Yes: D1 documents v2 schema, `version: 2` mandatory.
- [X] CHK002 Does the build matrix cover exactly 3 targets (darwin-arm64, linux-amd64, linux-arm64)? [Consistency] [FR-001, Data-model §Build matrix] — Yes: FR-001 lists all 3; data-model has the 6-binary matrix.
- [X] CHK003 Is `CGO_ENABLED=0` specified for BOTH GoReleaser AND Nix flake? [Consistency] [FR-002, FR-011] — Yes: FR-002 "CGO_ENABLED=0", FR-011 "CGO_ENABLED = '0' and the same ldflags."
- [X] CHK004 Are the ldflags exact and consistent between GoReleaser and Nix? [Consistency] [FR-002, FR-011] — Yes: FR-002 specifies `-s -w -X main.version={{.Version}}`; FR-011 says "the same ldflags as GoReleaser."
- [X] CHK005 Is `-trimpath` specified? [Completeness] [FR-002] — Yes: FR-002 lists `-trimpath` explicitly.
- [X] CHK006 Does the GoReleaser config produce BOTH `wa` and `wad` binaries? [Completeness] [FR-001] — Yes: FR-001 "builds for cmd/wa and cmd/wad."

## Notarization Pipeline

- [X] CHK007 Is the rcodesign workflow sign → notary-submit → staple? [Completeness] [Research D2] — Yes: D2 documents 3 steps explicitly.
- [X] CHK008 Are all 5 secrets documented? [Completeness] [Research D2] — Yes: cert, key, API key, key ID, issuer all listed in D2 and Assumptions.
- [X] CHK009 Does notarization failure block the release? [Edge Cases] [FR-008] — Yes: "MUST fail if notarization fails — un-notarized macOS binaries MUST NOT be published."
- [X] CHK010 Is `--code-signature-flags runtime` documented? [Completeness] [Research D2] — Yes: D2 says "sign with --code-signature-flags runtime (hardened runtime required for notarization)."

## Nix Flake Quality

- [X] CHK011 Is `vendorHash` (not deprecated `vendorSha256`) specified? [Modernity] [Research D3] — Yes: D3 says "vendorSha256 is deprecated; vendorHash is the current attribute."
- [X] CHK012 Is `subPackages` specified for both binaries? [Completeness] [FR-010, Research D3] — Yes: D3 says `subPackages = ["cmd/wa" "cmd/wad"]`.
- [X] CHK013 Is `meta.mainProgram` set? [Completeness] [Research D3] — Yes: D3 says `meta.mainProgram = "wa"`.
- [X] CHK014 Is Go version pinning documented? [Clarity] [Research D3] — Yes: D3 says "use buildGo126Module (nixpkgs provides version-specific builders)."

## Service Integration Correctness

- [X] CHK015 Is the launchd plist path exact? [Clarity] [FR-014, Contracts/service-files.md] — Yes: `~/Library/LaunchAgents/com.yolo-labz.wad.plist` in both.
- [X] CHK016 Is the systemd unit path exact? [Clarity] [FR-015, Contracts/service-files.md] — Yes: `~/.config/systemd/user/wad.service` in both.
- [X] CHK017 Does launchd use KeepAlive + RunAtLoad? [Completeness] [Research D4, Contracts] — Yes: both present in the contract plist template.
- [X] CHK018 Does systemd use Restart=on-failure + RestartSec=5s + WantedBy=default.target? [Completeness] [Research D5, Contracts] — Yes: all three in the contract unit template.
- [X] CHK019 Is loginctl enable-linger documented? [Completeness] [Research D5] — Yes: D5 says "required for headless operation — document as a post-install hint." Contract table includes it.
- [X] CHK020 Does service file use absolute binary path? [Clarity] [FR-019] — Yes: FR-019 "MUST reference the absolute path... (not a relative path)." Contract uses `{{WAD_PATH}}` resolved via `os.Executable()`.
- [X] CHK021 Does install-service refuse root? [Edge Cases] [FR-016] — Yes: "MUST refuse to run as root with a clear error."
- [X] CHK022 Is --dry-run specified? [Completeness] [FR-017] — Yes: "MUST print the generated service file to stdout without writing or loading it."
- [X] CHK023 Is uninstall-service idempotent? [Edge Cases] [FR-018] — Yes: "MUST be idempotent."

## Tooling Modernity (2026)

- [X] CHK024 Is GoReleaser v2 confirmed? [Modernity] [Research D1] — Yes: D1 explicitly says v2 with `version: 2` mandatory.
- [X] CHK025 Is rcodesign confirmed for Linux notarization? [Modernity] [Research D2] — Yes: D2 documents it as the modern Linux-native alternative.
- [X] CHK026 Is buildGoModule the current nixpkgs pattern? [Modernity] [Research D3] — Yes: D3 confirms `buildGoModule` with `vendorHash`; `buildGoPackage` is deprecated.
- [X] CHK027 Is git-cliff confirmed? [Modernity] [FR-022] — Yes: cliff.toml exists from feature 001; FR-022 references it.

## Cross-Artifact Consistency

- [X] CHK028 Do targets match CLAUDE.md §Distribution? [Consistency] — Yes: CLAUDE.md says "darwin-arm64, linux-amd64, linux-arm64"; FR-001 matches.
- [X] CHK029 Does Homebrew tap name match? [Consistency] [FR-005] — Yes: CLAUDE.md says `yolo-labz/homebrew-tap`; FR-005 says "publishing to yolo-labz/homebrew-tap."
- [X] CHK030 Do service log paths match CLAUDE.md? [Consistency] — Yes: CLAUDE.md §FS layout says `$XDG_STATE_HOME/wa/{wa.log,audit.log}`; contract uses `$XDG_STATE_HOME/wa/wad.log`.
- [X] CHK031 Does upgrade detection cover all 4 paths? [Completeness] [FR-020] — Yes: Cellar → brew, /nix/store/ → nix, "devel"/go-install → go install, fallback → URL.
- [X] CHK032 Is release trigger on `v*` tags consistent? [Consistency] [FR-006] — Yes: existing tags are v0.0.1 through v0.0.6; FR-006 triggers on `v*`.

## Notes

- All 32 items pass. Zero gaps found.
- The spec, research, contracts, and data-model are fully consistent across all checklist dimensions.
- CHK009 (notarization failure blocks release) is the most important item — verified explicitly in FR-008.
- No spec changes were needed — the requirements are already complete and unambiguous for a config-heavy feature.
