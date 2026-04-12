# Release Engineering Handoff — wa

**Date:** 2026-04-12
**PR merged:** #15 (feat: supply-chain attestations + CodeQL + OSV-Scanner + Scorecard)
**Tag pushed:** v0.4.0 (first supply-chain-attested release)
**Session:** NixOS repo rollout — 6-agent research + parallel execution

## What was shipped

- **CodeQL SAST** (Go autobuild + Actions, security-extended) — weekly + push + PR
- **OSV-Scanner** (replaces govulncheck — V2 invokes it internally for Go call-graph analysis) — SARIF mode, non-blocking
- **OpenSSF Scorecard** — weekly, SARIF upload to Security tab
- **Reproducibility** — GoReleaser snapshot build-twice-and-diff
- **cosign keyless signing** on release checksums via GoReleaser `signs:` stanza
- **CycloneDX 1.7 SBOM** via `cyclonedx-gomod app -licenses -std -json` + **SPDX 2.3** via syft
- **Build provenance attestations** via `actions/attest-build-provenance@v2` + `actions/attest-sbom@v2`
- **harden-runner** in audit mode on release workflow
- **Fuzz test** (`internal/domain/jid_fuzz_test.go`) for Scorecard Fuzzing credit
- **CODEOWNERS**, **CONTRIBUTING.md** updated, `.github/scorecard-config.yml`, `.github/actions-lock.md`
- All actions SHA-pinned with `# vX.Y.Z` comments
- Top-level `permissions: {}` on all new workflows, per-job re-grants
- govulncheck job removed from ci.yml (OSV-Scanner is a strict superset)
- Branch protection already existed (classic, required: lint/test/nix/sonar/commitlint)

## What the release pipeline produces (v0.4.0+)

Each tag push generates:
- Cross-compiled tarballs (darwin-arm64, linux-amd64, linux-arm64) + checksums.txt
- `checksums.txt.sigstore.json` (cosign keyless bundle)
- Per-binary CycloneDX 1.7 SBOMs (`wa_*_*.cdx.json`)
- SPDX 2.3 SBOM (`sbom.spdx.json`)
- GitHub Attestations (build provenance + SBOM) — verify with:
  ```
  gh attestation verify ./wa_0.4.0_darwin_arm64.tar.gz --repo yolo-labz/wa
  ```

## Completed post-merge (2026-04-12)

- **`HOMEBREW_TAP_GITHUB_TOKEN`** — fine-grained PAT set (scoped to yolo-labz org, `contents: write` on homebrew-tap). Next release will auto-bump the tap formula.
- **Go toolchain bumped to 1.26.2** — fixes 4 stdlib CVEs (GO-2026-4866 et al.)
- **Dependabot PRs merged** — action bumps triaged and landed
- **SonarQube** — project `yolo-labz_wa` already existed; token validated (`valid: true`)

## Nice-to-have (none blocking)

1. **Add required status checks** to branch protection — now that CodeQL, OSV-Scanner, Scorecard, reproducibility have all run on main, lock them as required (or migrate to a Repository Ruleset)
2. **Apple Developer ID secrets** — for macOS notarization (degrades gracefully when absent)
3. **1 open Dependabot PR** — Renovate may also be picking up action bumps; triage as they come

## Source of truth

- Research: `~/NixOS/meta/yolo-labz-release-engineering-research.md`
- Plan: `~/NixOS/meta/yolo-labz-release-engineering-plan.md`
- Global rule: `plugin-release-engineering` in `~/NixOS/modules/home/claude-code.nix`
- This repo uses: git-cliff (not release-please), Renovate (not Dependabot), lefthook, depguard, Apache-2.0
