# Pinned Action Versions

All GitHub Actions are pinned by full 40-char commit SHA with a trailing
`# vX.Y.Z` comment. Renovate (or Dependabot) preserves the comment when
bumping the SHA.

## Core actions

| Action | SHA | Version | Purpose |
|--------|-----|---------|---------|
| `actions/checkout` | `11bd71901bbe5b1630ceea73d27597364c9af683` | v4.2.2 | Repository checkout |
| `actions/setup-go` | `d35c59abb061a4a6fb18e82ac0862c26744d6ab5` | v5.5.0 | Go toolchain setup |
| `actions/upload-artifact` | `ea165f8d65b6e75b540449e92b4886f43607fa02` | v4.6.2 | Artifact upload |
| `actions/download-artifact` | `d3f86a106a0bac45b974a628896c90dbdf5c8093` | v4.3.0 | Artifact download |

## Security & supply-chain

| Action | SHA | Version | Purpose |
|--------|-----|---------|---------|
| `github/codeql-action/*` | `f35333b910470a5408cb081b68f0701254a7d27b` | v3.28.18 | CodeQL SAST + SARIF upload |
| `google/osv-scanner-action` | `c51854704019a247608d928f370c98740469d4b5` | v2.3.5 | OSV vulnerability scanning (replaces govulncheck) |
| `ossf/scorecard-action` | `99c09fe975337306107572b4fdf4db224cf8e2f2` | v2.4.3 | OpenSSF Scorecard |
| `step-security/harden-runner` | `f808768d1510423e83855289c910610ca9b43176` | v2.17.0 | Runner network/process hardening |
| `actions/attest-build-provenance` | `e8998f949152b193b063cb0ec769d69d929409be` | v2.4.0 | SLSA build provenance attestation |
| `actions/attest-sbom` | `bd218ad0dbcb3e146bd073d1d9c6d78e08aa8a0b` | v2.4.0 | SBOM attestation |

## Build & release

| Action | SHA | Version | Purpose |
|--------|-----|---------|---------|
| `goreleaser/goreleaser-action` | `e435ccd777264be153ace6237001ef4d979d3a7a` | v6.4.0 | GoReleaser build + release |
| `golangci/golangci-lint-action` | `25e2cdc5eb1d7a04fdc45ff538f1a00e960ae128` | v8.0.0 | Go linting |
| `SonarSource/sonarqube-scan-action` | `fd88b7d7ccbaefd23d8f36f73b59db7a3d246602` | v6.0.0 | SonarQube analysis |
