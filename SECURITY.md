# Security Policy

This project automates a personal WhatsApp account. The threat model is asymmetric: a single ban or session leak is high-cost, and the project may be invoked by a large language model on behalf of the user. Read this document before deploying anything.

## Threat model

| # | Threat | Impact | Mitigation |
|---|---|---|---|
| T1 | Prompt injection from inbound WhatsApp messages | A malicious contact tells the assistant to DM the user's boss, exfiltrate secrets, or run shell commands | All inbound message bodies must be wrapped in `<untrusted-sender>…</untrusted-sender>` tags before reaching the model. The Claude Code plugin's `PreToolUse` hook on `Bash` re-validates every `wa send --to` invocation against the allowlist before letting the model send anything. |
| T2 | Malicious allowlisted contact | A trusted contact's account is compromised and used to drive the assistant | Per-action allowlist (`read` ≠ `send` ≠ `group.add`); rate limiter that caps per-day volume; manual review of audit log; fast `wa allow remove <jid>` |
| T3 | Lost or stolen laptop | Whoever holds the unlocked machine can impersonate the user on WhatsApp | FileVault is the documented baseline. Session DB is `chmod 0600`. `wa panic` unlinks the device server-side. Re-pairing requires the user's phone. |
| T4 | Supply-chain compromise of `whatsmeow` upstream | A backdoored release sends messages, exfiltrates ratchets, or installs persistence | Pin `go.mau.fi/whatsmeow` by commit (it has no semver tags). Renovate/Dependabot review every bump. `govulncheck` runs in CI. Reproducible builds via `-trimpath`. |
| T5 | WhatsApp account ban | The number is locked, mobilization stops, key material is invalidated | Non-overridable rate limiter, automatic warmup on fresh sessions, refusal of high-risk operations (broadcast lists, mass group adds), audit log to detect runaway loops |
| T6 | Local privilege escalation via the unix socket | Another local user reads or writes to `wa.sock` | Socket is `chmod 0600`, lives under `$XDG_RUNTIME_DIR` (per-user directory), and uses `LOCAL_PEERCRED`/`SO_PEERCRED` to reject any UID other than the owner on accept |
| T7 | Session DB on disk in plaintext | Backups, cloud sync, or another process reads Signal ratchets | The DB lives only under `$XDG_DATA_HOME/wa/`; documented as FileVault-only; never committed to git; `wa session export` produces an age-encrypted tarball for backup |

## Supply-chain posture

The project ships SLSA Build **L2** native attestations and a multi-format SBOM bundle on every release tag. The full asset set per release:

| Asset | Generator | Verifier |
|---|---|---|
| `wa_<version>_<os>_<arch>.tar.gz` | GoReleaser v2.5 | `sha256sum -c checksums.txt` |
| `wa_<version>_linux_<arch>.{deb,rpm,apk}` | GoReleaser nfpms | `sha256sum -c checksums.txt` |
| `checksums.txt.sigstore.json` | Cosign v3 sign-blob | `cosign verify-blob --bundle ...` or `gh attestation verify` |
| `sbom.cdx.json` | syft (CycloneDX 1.6) | any CycloneDX consumer |
| `sbom.spdx.json` | syft (SPDX 2.3) | any SPDX consumer |
| `sbom.gomod.{wa,wad}.cdx.json` | cyclonedx-gomod (per binary, with stdlib + license info) | any CycloneDX consumer |
| Build provenance attestation | `actions/attest-build-provenance@v4.1.0` | `gh attestation verify --owner yolo-labz` |
| SBOM attestation (CycloneDX) | `actions/attest-sbom@v4.1.0` | `gh attestation verify` |
| SBOM attestation (SPDX) | `actions/attest-sbom@v4.1.0` | `gh attestation verify` |

**SLSA L2 ceiling.** L3 requires job isolation (separate signing job, no shared cache, dedicated OIDC identity). The single-host blast radius of `wa` does not justify the operational complexity. Stay at L2 + native attestations until an external L3-enforcing consumer appears.

**Recommended verify-before-install.** Pin to a specific tag and run `gh attestation verify checksums.txt --owner yolo-labz` before unpacking the tarball (see `README.md` §Install).

## Static analysis lanes (defense in depth)

| Lane | Tool | What it catches |
|---|---|---|
| Code | CodeQL `security-extended` (Go + GitHub Actions) | OWASP Top 10, taint flows, injection patterns |
| Dependencies | OSV-Scanner V2 (with internal govulncheck for Go call-graph reachability) | Known CVEs, vuln-DB matches |
| Secrets | gitleaks (PR-diff + push-full) | High-confidence secret patterns. **Note:** Sonar Community Build does not include the 400+ secret-pattern engine — that lives in Developer Edition. gitleaks is the canonical lane. |
| Workflows | actionlint + zizmor (audit mode → enforce after first clean run) | Template injection, unpinned third-party actions, over-broad `permissions:`, cache-poisoning |
| Reproducibility | `goreleaser release --snapshot` × 2 + checksum diff | Byte-identity drift on every PR |
| Supply chain | OpenSSF Scorecard (weekly cron) | Pinned-dependencies, signed releases, branch protection, code-review |
| Quality | SonarQube Server 25.x | Cognitive complexity, code smells, duplications, Sonar Way for Go |

## Bleeding-edge gaps tracked but not yet implemented (2026-04-26 audit)

A research swarm on 2026-04-26 surfaced five additional supply-chain items beyond what's already shipped. Each is intentionally NOT yet wired so it can be implemented under proper review:

- **OpenVEX statements per release** — `vexctl` + `openvex/go-vex` would emit a `.openvex.json` alongside the existing SBOMs declaring the exploitability status of each transitive CVE. Most should be `not_affected` due to the depguard-enforced port boundary; that's the value-add. Tracked.
- **`gomodguard` exact-path allowlist** — defends against Go Module Proxy cache-persistence typosquats (BoltDB GO-2025-3451, qiniiu/qmgo MongoDB lookalike). Needs local `golangci-lint run` validation against the actual import set before landing.
- **GoReleaser v2.5.1 → v2.6.1+** — Sigstore bundles on the nfpm packages + deterministic source-archive ordering. Current `version: v2.5.1` pin is pre-feature.
- **diffoscope 313+ reproducibility-diff CI job** — already on the `Reproducibility` workflow's lane but the recipe predates the Arch Linux 2026-01-22 Go-binary updates (pin toolchain in `go.mod`, normalise `GOMODCACHE`).
- **Anchore Quill** as fallback alternative to `rcodesign` for darwin notarization, documented in case rcodesign upstream stalls.

Full analysis: `~/Documents/Notes/wa-improvement-loop/research/06-bleeding-edge-2026Q2-gaps.md`. **TUF and Sigsum are explicitly rejected** (scope creep without an auto-updater, which the project rule §10 forbids).

## Scorecard caveats (structural caps)

The Scorecard score is currently `7.4/10` with two structural zero-scoring checks that the loop **cannot fix**:

- **Code-Review (0/10):** the maintainer-team policy requires one CODEOWNER approval on every PR. As a solo-maintained repo, the scorecard reads `1/30 approved changesets — score normalized to 0`. The gate exists in branch-protection but is bypassed via `--admin` for self-merges. This is a known structural cap; document but do not paper-over.
- **Maintained (0/10):** the repo was created within the last 90 days. Auto-heals at day 90 with ≥1 commit/week.
- **CII-Best-Practices (0/10):** requires the maintainer to fill out the OpenSSF Best Practices questionnaire at <https://www.bestpractices.dev/>. Free win, manual action required.

Realistic ceiling for this project's solo-maintainer profile is ~8.7/10. Document the deltas in this file rather than gaming the score.

## Reporting a vulnerability

This is a personal project; there is no bug-bounty programme. Email the maintainer at the address in `git config user.email`. Do not open public issues for security problems.

## Out of scope

- Bulk messaging, marketing automation, group spam — these are not threat-modeled because they are not supported.
- Multi-tenant deployments, hosted SaaS, web UI on a public IP — same.
- Cloud API features that require Meta Business verification — this project does not target the official API.

## What you must not do

- Do not request `Bash(*)` or `Bash(wa:*)` permission in any Claude Code plugin that wraps this CLI. Enumerate exact subcommands.
- Do not bypass the rate limiter. There is no `--force` flag and there will not be one.
- Do not commit `session.db`, `allowlist.toml`, `.envrc.local`, or anything from `$XDG_STATE_HOME/wa/` to git.
- Do not run `wad` as root or under a service account that has write access to other users' home directories.
