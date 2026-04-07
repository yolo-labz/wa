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
