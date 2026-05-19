# Feature 110e — Contracts: `wa pair --remote` CLI flag

Sole contract is a cobra-flag surface on the existing `wa pair` subcommand. No JSON-RPC method added.

## Flag

| Flag | Type | Default | Spec FR |
|---|---|---|---|
| `--remote <host>:<app>` | string | `""` (disabled) | FR-001, FR-002 |

When `--remote` is non-empty, `wa pair` short-circuits its existing JSON-RPC unix-socket path and instead `exec`s the SSH chain. When empty, behaviour is unchanged from today.

## `wa pair --help` excerpt (target shape)

```
Usage:
  wa pair [flags]

Pair with WhatsApp by scanning a QR code or entering a phone number.

Flags:
      --browser              Open the QR code in your default browser (recommended)
      --phone string         E.164 phone number for phone-code pairing flow
      --idempotency-key      FR-034a replay key (same key + params replays cached result)
      --remote string        Drive a remote daemon over SSH. Format: <ssh-host>:<dokku-app>
                             Example: --remote ProxMox.Dokku:wa-burocracy
                             SSH host may be a ~/.ssh/config alias, FQDN, or user@host.
                             Refuses HTTP/HTTPS URL form — pair requires SSH access to
                             the daemon's host, not the REST endpoint.
  -h, --help                 help for pair
```

## Flag interaction matrix

| Combination | Behaviour | Spec FR |
|---|---|---|
| `--remote x:y` alone | Exec `ssh -t x dokku enter y -- /usr/local/bin/wa pair`. QR renders in operator's terminal. | FR-001 |
| `--remote x:y --browser` | Exec `ssh -t x dokku enter y -- /usr/local/bin/wa pair --browser`. **The browser opens on the operator's local machine** because daemon-side `--browser` writes HTML inside the container, but the QR data is rendered in the operator's terminal regardless. (Future enhancement: SSH-forward the in-container `wa-pair.html` path to the local browser — explicitly OUT OF SCOPE per spec §"Out of scope".) | FR-004 |
| `--remote x:y --phone +5511999999999` | Exec the chain with `--phone` appended. Daemon returns a phone-pair code; SSH chain prints it to the operator's terminal. | FR-005 |
| `--remote x:y --idempotency-key abc123` | Exec the chain with `--idempotency-key abc123` appended. Daemon caches result per FR-034a. | FR-005 |
| `--remote https://...` | Refuse with exit 64 + actionable message: `"wa pair --remote: pair requires SSH access to the daemon's host, not the REST endpoint. Use --remote <ssh-host>:<dokku-app> instead — e.g. --remote ProxMox.Dokku:wa-burocracy."` | FR-003 |
| `--remote ""` (explicit empty) | Treated as absent. Existing socket path. | FR-008 |
| `--remote x:y --socket /tmp/foo.sock` | `--socket` is ignored when `--remote` is non-empty. Cobra has no built-in mutual-exclusion enforcement here; documented as "remote wins". No error. | FR-008 |
| `--remote x:y --remote-url https://...` | This combination does NOT EXIST — the 110c REST CLI mode uses the SAME `--remote` global flag with a URL value. Pair refuses the URL form by inspecting the value (FR-003), so the URL-shape can never reach pair's exec. | FR-008 |

## Exit-code contract

Inherits the SSH-chain exit code on success/failure. The translation table:

| Condition | Exit |
|---|---|
| Successful pair (daemon-side success) | 0 |
| Operator cancels (Ctrl-C) | 130 (SIGINT) |
| Malformed `--remote` value | 64 (EX_USAGE) — caught by `ParseRemoteTarget` |
| `--remote https://...` URL form | 64 (EX_USAGE) — same path |
| SSH host not resolvable | 255 (passed through from `ssh`) |
| Dokku app missing on host | 1 (passed through from `dokku enter`) |
| `ssh` binary not in PATH | 70 (EX_SOFTWARE) — `runPairRemote` short-circuit |
| Daemon-side pair refused (already paired) | inherits whatever exit `wa pair` returns inside the container (no remap) |

## Backwards compatibility contract

- Every invocation of `wa pair` **without** the new flag MUST behave bit-for-bit identically to the pre-110e binary.
- The `wa pair` JSON output schema is unchanged (`wa.pair/v1`). 110e adds zero new schema versions.
- The daemon-side `pair` JSON-RPC method is unchanged.

## Output contract (passthrough)

When `--remote` routes through SSH:

- `stdout` from the in-container `wa pair` is forwarded to the operator's `stdout`. NDJSON, QR escapes, and operator hints all flow.
- `stderr` is forwarded similarly. `slog` warnings, SSH banner, dokku-enter banner all appear on the operator's `stderr`.
- `stdin` is forwarded. Currently unused by `wa pair` but reserved for future phone-code-entry interactivity.

`--json` flag, when passed alongside `--remote`, is forwarded into the SSH chain so the in-container `wa pair` emits NDJSON. The wrapping `ssh -t` does NOT mangle the stream because the operator's terminal is the consumer; if `--json` output is being piped, operators should use `ssh` directly (current 110c guidance).
