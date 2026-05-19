# Feature 110e — `wa pair --remote` UX

**Branch**: `147-wa-pair-remote`
**Status**: drafted
**Source**: 09/05/2026 memory-leak + dokku investigation surfaced the gap. Pedro asked: "Check if we can integrate the pair with remote easily. Maybe add that feature." Direct extension of `110c-wa-remote-cli` (general REST transport for `wa`) — pair was deliberately excluded from 110c because its interactive QR render does not fit the request/response REST shape.

## Problem

When a `wa-burocracy` or `wa-personal` daemon loses its WhatsApp linked-device state (phone-side unlink, post-rebuild divergence, multi-day disconnect that triggers server-side cleanup), the only documented re-pair path is:

```bash
ssh -t dokku.example.com 'dokku enter wa-burocracy -- /usr/local/bin/wa pair'
```

Three frictions:

1. **Multi-step memorisation.** Operator must remember the SSH hostname, the dokku app name, the `dokku enter` invocation, and the full path of `wa` inside the distroless image.
2. **No multi-host friendliness.** `wa-remote` (110c) cannot drive pair because it forwards a unix-domain socket whose mode `srw------- 65532:65532` blocks the SSH user (`notroot` on the Proxmox host) from reading. The forward returns `permission denied` even when the operator has working SSH access.
3. **Surface inconsistency.** Every other operator action against a remote daemon is `wa --remote https://wa.example.com <verb>`; pair is the one verb that requires a completely different idiom (`ssh -t dokku enter`).

The friction matters now: after the 19/05/2026 wa-burocracy rebuild, Pedro observed daemon-reported `paired:true connected:true` but his phone treated the device as unlinked. The re-pair path he needed was 70+ characters of nested SSH + dokku verbs. Same friction will recur every time WhatsApp prunes a linked-device entry (months-of-idle daemon, multi-account rotation, restored-from-backup phone).

## Decision

Add `wa pair --remote <host>:<app>` flag that wraps the SSH + dokku-enter chain inline. Renders QR locally via the existing `mdp/qrterminal/v3` half-block stream over an SSH PTY. Optionally pairs with the existing `--browser` flag to open the QR HTML page on the operator's machine.

### Surface

```bash
# 1:1 dokku-host:app remote pair (QR in terminal)
wa pair --remote ProxMox.Dokku:wa-burocracy

# Same, opening the QR HTML page in the operator's default browser
wa pair --remote ProxMox.Dokku:wa-burocracy --browser

# Phone-code pairing (no QR), remote
wa pair --remote ProxMox.Dokku:wa-burocracy --phone +5511999999999
```

`--remote` takes a single `<host>:<app>` string. `host` is anything `ssh` can resolve (Tailscale aliases, `~/.ssh/config` Host entries, full FQDNs, `user@host`). `app` is the dokku app name (`wa-burocracy`, `wa-personal`, etc.).

### Auth shape

SSH keys. No bearer-token plumbing on the pair path — pair is intentionally an out-of-band operator action and inherits the same trust model the operator already has for `ssh -t dokku.example.com dokku enter`. Token-based pair was rejected (see Alternatives §A).

### URL-style escape hatch (Alternatives §C)

`--remote https://wa.example.com` is **refused** with exit code 64 and an actionable message:

```
wa pair --remote: pair requires SSH access to the daemon's host, not the REST endpoint.
Use --remote <ssh-host>:<dokku-app> instead — e.g. --remote ProxMox.Dokku:wa-burocracy.
```

This protects operators who type `wa pair --remote https://...` by analogy with `wa send --remote https://...` and would otherwise see a confusing `dokku enter` error from `ssh`.

### Implementation outline (informative, not normative for this spec)

`cmd/wa/cmd_pair.go` adds a `pairRemote string` cobra flag. When non-empty, the existing pair body short-circuits: `cmd_pair.go` exec's `ssh -t <host> dokku enter <app> -- /usr/local/bin/wa pair <extra-flags>` with the remaining flags (`--phone`, `--browser`, `--idempotency-key`) preserved. PTY allocation (`-t`) is mandatory so the half-block QR renders. Stdin/stdout/stderr inherit, so QR escapes and operator's terminal flow through unmodified.

## Alternatives rejected

Per Constitution rule 20 (Nygard ADR / MADR completeness).

### A. Token-based pair over REST + WebSocket QR stream

Add a `pair` JSON-RPC method to the REST adapter (110a) that returns the QR text in a streaming response. Operator hits `wa --remote https://wa.example.com pair`, no SSH needed.

**Rejected** because:

1. Pair is fundamentally interactive — operator must scan within the QR rotation window (~20 s). Streaming over an HTTP response (or SSE, or WebSocket) introduces a second transport path with its own timeout, reconnect, and back-pressure semantics, doubling the test surface for an action operators run twice a year.
2. Token plumbing for pair raises chicken-and-egg questions: a freshly rebuilt daemon may not yet have a token in `tokens.db`. Bootstrap-via-pair becomes a recursive problem.
3. SSH-keyed pair is already the trust model the operator has — they own the host's SSH keys because they own the dokku deploy. Reusing that beats inventing a parallel REST-token bootstrap.

### B. Run the SSH chain via `wa-remote pair`

Extend the existing `scripts/wa-remote` bash helper with a `pair` special case that switches from socket-forward to `ssh -t dokku enter`.

**Rejected** because:

1. Two transport paths inside one script is a maintenance hazard — every flag added to wa-remote has to decide which path applies.
2. Bash + SSH + dokku-enter argument escaping is error-prone (`--phone +5511...` has a `+` that some shells mangle); doing it in Go where `os/exec` `Command(name, args...)` does not invoke a shell is safer.
3. Bash helper exists for the unprivileged-socket forward; pair is conceptually a different kind of remote action and deserves a CLI surface on the binary itself.

### C. Detect URL vs `host:app` in a single `--remote` flag

`wa pair --remote ProxMox.Dokku:wa-burocracy` and `wa send --remote https://wa.example.com` share one flag whose value is sniffed (starts with `http`/`https` → REST transport, otherwise → SSH transport).

**Rejected** because:

1. `--remote` already has a documented meaning in 110c (REST URL). Overloading it for pair only makes the help text contradictory.
2. The `host:app` form is pair-specific. Other subcommands have no `:app` semantics. Reusing one flag across two distinct value shapes is a sniff-rule trap.
3. Two distinct concepts deserve distinct flags. Inversely, `wa send` does not need a `host:app` form — the REST endpoint already routes by host.

The chosen approach scopes `--remote <host>:<app>` to `wa pair` only (Section "Surface" above), leaving `wa --remote <url>` semantics unchanged for every other subcommand.

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-001 | `wa pair --remote <host>:<app>` SHALL exec `ssh -t <host> dokku enter <app> -- /usr/local/bin/wa pair` with all other passed flags preserved verbatim. | `testscript` harness with stub `ssh` echo'ing argv; assert argv equals expected. |
| FR-002 | `wa pair --remote` with an invalid value (missing `:`, empty host, empty app) SHALL exit 64 with a message naming the expected format. | Unit test on the validation function. |
| FR-003 | `wa pair --remote https://...` SHALL refuse with exit 64 and an actionable message pointing the operator at `<host>:<app>`. | Unit test asserting exit code + stderr substring. |
| FR-004 | `wa pair --remote` with `--browser` SHALL forward `--browser` to the remote `wa pair` invocation, but SHALL open the browser on the operator's local machine (existing 110c semantics — daemon stays headless). The daemon writes the HTML to a known path inside the container; the SSH forwarding of that path to the local browser is OUT OF SCOPE for this spec and is a follow-up. | Manual integration test on a paired host. |
| FR-005 | `wa pair --remote` SHALL preserve `--phone <E164>` and `--idempotency-key <key>` flags through the SSH chain. | Argv-capture test. |
| FR-006 | When `ssh` binary is absent on the operator's machine, `wa pair --remote` SHALL exit 70 (internal error) with a message naming the missing binary. | Unit test with `PATH=` pointing to an empty tmpdir. |
| FR-007 | `wa pair --remote` SHALL NOT introduce any new daemon-side change. The wad binary running on the dokku host is identical to today. | Code review: no diff under `internal/adapters/secondary/whatsmeow/pair*.go`. |
| FR-008 | The spec 110c `--remote https://...` flag SHALL continue to refuse the `pair` subcommand with exit 64 (existing behaviour preserved). | Existing test in 110c regression suite. |

## User scenarios & testing

### Primary user story

Pedro operates two `wad` deployments on his Proxmox-hosted dokku (`wa-personal`, `wa-burocracy`). After a multi-day daemon outage or a phone-side device-prune, he runs:

```bash
wa pair --remote ProxMox.Dokku:wa-burocracy
```

His terminal renders the QR (half-block UTF-8). He opens WhatsApp → Settings → Linked Devices → Link Device → scans. Daemon catches the `events.PairSuccess`. Command exits 0. Phone shows the device as linked.

**Time-to-pair budget:** under 30 seconds wall-clock from typing the command to seeing "paired" output, assuming SSH key is loaded and operator is at their phone.

### Secondary user story — phone-code path

Pedro is in a context where scanning a QR is impractical (over an SSH session from a phone, terminal does not render half-blocks, etc.). He runs:

```bash
wa pair --remote ProxMox.Dokku:wa-burocracy --phone +5511999999999
```

Command prints an 8-character pair code. He enters it on WhatsApp → Linked Devices → Link with phone number. Daemon catches the success event. Command exits 0.

### Tertiary user story — wrong-shape `--remote`

Pedro mistypes `--remote https://wa-burocracy.home301server.com.br` (the public health endpoint, not the dokku-app shape). Command exits 64 immediately with:

```
wa pair --remote: pair requires SSH access to the daemon's host, not the REST endpoint.
Use --remote <ssh-host>:<dokku-app> instead — e.g. --remote ProxMox.Dokku:wa-burocracy.
```

He retypes with the correct shape and proceeds.

### Edge cases

- **SSH host alias resolution fails.** SSH itself emits `Could not resolve hostname`; `wa pair` propagates the exit code (SSH typically exits 255). No special handling in `wa pair`.
- **Dokku app missing on host.** `dokku enter` exits non-zero with `app does not exist`; SSH forwards that to `wa pair`'s exit. No special handling.
- **Operator's SSH key not loaded.** SSH prompts for password — operator sees it on their local terminal because of `-t`. If they cannot authenticate, SSH exits non-zero; `wa pair` propagates.
- **Daemon already paired.** Remote `wa pair` returns the existing "already paired" error (current behaviour); operator must run `wa session logout-all --remote ...` or `wa panic --remote ...` first (those subcommands are out of scope for this spec; they exist today on 110c REST or via `dokku enter`).

## Success criteria

| Criterion | Metric | How to measure |
|-----------|--------|----------------|
| SC-001 | Operator can complete a re-pair from a fresh shell in ≤ 30 s | Stopwatch from first keypress of `wa pair --remote ...` to QR-scan-success on phone, with SSH keys already in agent. |
| SC-002 | Zero new daemon-side changes | `git diff origin/main -- internal/` returns empty for the daemon-side packages. |
| SC-003 | Help text discoverability | `wa pair --help` shows `--remote <host>:<app>` with a one-line description and an example. |
| SC-004 | Re-pair path is documented in `docs/deploy/dokku.md` | A new sub-section "Re-pair from a remote workstation" exists and references `wa pair --remote`. |
| SC-005 | Backwards compatibility | All existing `wa pair` invocations (no `--remote` flag) continue to work identically. Regression test suite for cmd_pair stays green. |

## Assumptions

1. The operator already has SSH access to the dokku host. This spec does not address SSH key distribution.
2. The dokku host has the `wa` binary at `/usr/local/bin/wa` inside the deployed container (already true for current `Dockerfile`).
3. The operator's terminal can render UTF-8 half-blocks. If not, the operator uses `--phone` instead.
4. Browser auto-open (`--browser`) targets the LOCAL machine. Remote-host browser is not a use case (the host is a headless server).
5. Pair flow is operator-initiated and never automated. CI never pairs.

## Out of scope

- Bootstrapping a freshly-deployed daemon's REST token (`tokens.db` empty). Separate feature; would belong in 110d follow-up.
- A web UI for pair. The QR-in-browser path (`--browser`) writes a local HTML file; a full web dashboard is a different feature.
- Forwarding the daemon's `pairing.required` event to the operator's terminal automatically (push-based re-pair). Operator triggers manually for now.
- A multi-app fan-out (`wa pair --remote host:wa-personal,wa-burocracy`). YAGNI; pair is one-account-at-a-time by WhatsApp's own protocol.

## Out-of-band notes

- 19/05/2026 — Pedro experienced the friction live. wa-burocracy daemon reported `paired:true connected:true` post-rebuild, but his phone showed the device as unlinked. The current re-pair command `ssh -t ProxMox.Dokku 'dokku enter wa-burocracy -- /usr/local/bin/wa panic && /usr/local/bin/wa pair'` is the prototype that this feature replaces with `wa pair --remote ProxMox.Dokku:wa-burocracy` (and a separate `wa panic --remote ...` if a full wipe is desired).
- The implementation may share helpers with a future `wa panic --remote` and `wa session logout-all --remote` — both are extensions of the same SSH-chain pattern.

## References

- `specs/110c-wa-remote-cli/spec.md` — the 110c REST CLI mode whose surface this extends.
- `docs/deploy/dokku.md` — existing `dokku enter wa -- /usr/local/bin/wa pair` runbook.
- `cmd/wa/cmd_pair.go` — pair subcommand body.
- `scripts/wa-remote` — bash helper for socket forwarding (not used for pair).
- 09/05/2026 memory-leak + dokku investigation dossier at `~/Documents/Notes/2. Areas/wa-memory-investigation-2026-05-09/00-mission.md`.
