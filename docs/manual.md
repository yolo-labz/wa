# wa — User Manual

**Version**: v0.2.0
**Scope**: Complete reference for the `wa` CLI and `wad` daemon across installation, pairing, multi-profile workflows, safety, and every subcommand.

This manual is hand-maintained. Each subcommand's flag set is cross-checked against the live `wa --help` output; if you notice drift, open an issue or regenerate from the built binary.

---

## Table of contents

1. [Concepts](#1-concepts)
2. [Installation](#2-installation)
3. [First run — pairing](#3-first-run--pairing)
4. [Multi-profile cookbook](#4-multi-profile-cookbook)
5. [Global flags](#5-global-flags)
6. [Subcommand reference](#6-subcommand-reference)
7. [Output schemas (NDJSON)](#7-output-schemas-ndjson)
8. [Exit codes](#8-exit-codes)
9. [Filesystem layout](#9-filesystem-layout)
10. [Allowlist and rate limiter](#10-allowlist-and-rate-limiter)
11. [Audit log](#11-audit-log)
12. [Service installation](#12-service-installation)
13. [Migration and backups](#13-migration-and-backups)
14. [Troubleshooting](#14-troubleshooting)

---

## 1. Concepts

`wa` is a **two-binary system**:

| Binary | Role | Runs as |
|---|---|---|
| **`wad`** | Long-running daemon. Owns the WhatsApp session, the SQLite ratchet store, and the websocket to `web.whatsapp.com`. One `wad` process per profile. | `systemd` user unit (Linux), `launchd` agent (macOS), or a NixOS system service. Never root. |
| **`wa`** | Thin JSON-RPC client. Speaks to `wad` over a unix domain socket. This is what shell scripts, cron jobs, and Claude Code plugins actually invoke. | On demand. Idempotent, re-invokable thousands of times per day. |

The split exists because a WhatsApp multi-device session holds **Signal Protocol ratchets** + an **active websocket** + an **app-state cursor**. Re-pairing per message would cost 2–5 s of handshake, advance the ratchet, and look like a reconnect storm to WhatsApp's anti-abuse systems. The daemon owns this state for the whole session; the CLI is stateless glue.

A **profile** is a named isolation boundary. Each profile runs its own `wad` process with its own `session.db`, `allowlist.toml`, `audit.log`, rate limiter, and unix socket. Default install has one profile named `default` and users who never touch the `--profile` flag see it silently.

---

## 2. Installation

### Homebrew (macOS + Linux)

```bash
brew install yolo-labz/tap/wa
# Publishes once HOMEBREW_TAP_GITHUB_TOKEN is configured upstream.
```

### Nix flake (NixOS, nix-darwin, nix profile)

```bash
# One-shot run
nix run github:yolo-labz/wa -- profile list

# Install to your profile
nix profile install github:yolo-labz/wa

# Dev shell (go + gopls + golangci-lint + goreleaser + sqlite)
nix develop github:yolo-labz/wa
```

On **NixOS** use the module:

```nix
{
  inputs.wa.url = "github:yolo-labz/wa";
  outputs = { self, nixpkgs, wa, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        wa.nixosModules.default
        {
          services.wa.enable = true;
          services.wa.profile = "default";   # or "work", "personal", …
          services.wa.logLevel = "info";
        }
      ];
    };
  };
}
```

On a **home-manager** user profile:

```nix
{
  inputs.wa.url = "github:yolo-labz/wa";
  outputs = { self, nixpkgs, home-manager, wa, ... }: {
    homeConfigurations."me@myhost" = home-manager.lib.homeManagerConfiguration {
      modules = [
        wa.homeManagerModules.default
        {
          services.wa.enable = true;
          services.wa.profile = "default";
          services.wa.autoStart = true;
        }
      ];
    };
  };
}
```

Multiple profiles: import the module twice in different nixosConfigurations or use a separate home-manager module instance per profile.

### GoReleaser tarball

```bash
VERSION=v0.2.0
ARCH=linux_amd64   # or darwin_arm64 / linux_arm64
curl -LO "https://github.com/yolo-labz/wa/releases/download/$VERSION/wa_${VERSION#v}_${ARCH}.tar.gz"
curl -LO "https://github.com/yolo-labz/wa/releases/download/$VERSION/checksums.txt"
sha256sum -c checksums.txt --ignore-missing
tar xzf "wa_${VERSION#v}_${ARCH}.tar.gz"
install -m 0755 wa wad ~/.local/bin/
```

### `go install`

```bash
go install github.com/yolo-labz/wa/cmd/wa@v0.2.0
go install github.com/yolo-labz/wa/cmd/wad@v0.2.0
```

This produces binaries without the `X main.version` ldflag, so `wa version` reports a commit hash instead of a semver tag. The GoReleaser and Nix paths inject the tag correctly.

### Verifying the install

```bash
wa version
wad --help
ls -la "$XDG_RUNTIME_DIR/wa/" 2>/dev/null || echo "no sockets yet (daemon not started)"
```

---

## 3. First run — pairing

1. **Start the daemon** (once per profile):
   ```bash
   wad                           # default profile, foreground
   # OR
   wad install-service           # install systemd/launchd unit
   systemctl --user start wad@default.service    # Linux
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.yolo-labz.wad.default.plist  # macOS
   ```

2. **Pair your phone** (one-time per profile):
   ```bash
   wa pair                       # QR in terminal (default)
   wa pair --phone +5511999999999   # phone-code flow
   ```
   On your phone: WhatsApp → Settings → Linked Devices → Link a Device → scan the QR or enter the 8-character code.

3. **Check the connection**:
   ```bash
   wa status
   # Connected as 5511999999999@s.whatsapp.net
   ```

4. **Allow at least one recipient** (default-deny policy):
   ```bash
   wa allow add 5511999999999@s.whatsapp.net --actions send
   ```

5. **Send a test message**:
   ```bash
   wa send --to 5511999999999@s.whatsapp.net --body "hello from wa"
   ```

---

## 4. Multi-profile cookbook

### Add a second profile

```bash
wa profile create work
wad --profile work &
wa --profile work pair
wa --profile work allow add <work-jid> --actions send
wa --profile work send --to <work-jid> --body "hello from work"
```

### List and switch

```bash
wa profile list
# PROFILE   ACTIVE  STATUS      JID                         LAST_SEEN
# default   *       connected   5511999999999@s.whatsapp.net 2026-04-11T17:00:00Z
# work              connected   5511888888888@s.whatsapp.net 2026-04-11T17:00:00Z

wa profile use work
wa status                       # now targets the work profile
```

### Profile precedence (FR-001)

When a subcommand needs a profile, `wa` picks one via this chain:

1. `--profile <name>` flag
2. `WA_PROFILE` environment variable (empty string = unset)
3. `$XDG_CONFIG_HOME/wa/active-profile` file contents (whitespace + BOM trimmed)
4. Singleton: if exactly one profile exists, use it
5. Literal `default`

If multiple profiles exist and none of 1–3 are set, `wa` exits with code 78 and tells you to pick one.

### Remove a profile

```bash
wa profile rm work --yes
```

Hard constraints: cannot remove the active profile, the only profile, or a profile whose daemon is currently running. **There is no `--force` flag** (constitution §III).

---

## 5. Global flags

Every `wa` subcommand accepts these persistent flags:

| Flag | Type | Default | Description |
|---|---|---|---|
| `--profile <name>` | string | see §4 | Profile name. Must match `^[a-z][a-z0-9-]{0,30}[a-z0-9]$` and not be reserved. |
| `--socket <path>` | string | derived from profile | Override the unix socket path. Normally you do not set this — the profile flag derives the correct path. |
| `--json` | bool | false | Output NDJSON (see §7) instead of human-readable text. Every object carries a `schema` field. |
| `--verbose` | bool | false | Verbose RPC + client-side logs. |
| `-h, --help` | bool | — | Print command-specific help. |

`WA_LOG_LEVEL=debug wad` boosts daemon log verbosity (`debug`, `info`, `warn`, `error`).

---

## 6. Subcommand reference

### `wa pair`

Pair with WhatsApp by scanning a QR code or entering a phone-pairing code.

```
Usage:
  wa pair [flags]

Flags:
      --phone string   E.164 phone number for phone-code pairing flow
```

**QR flow (default)** — terminal displays a scan code; open WhatsApp on your phone → Settings → Linked Devices → Link a Device.

**Phone-code flow** — `--phone +5511999999999` causes wa to emit an 8-char code; enter it on your phone under Link a Device → Link with phone number.

Refuses to run if a session already exists. Use `wa panic` to wipe the session first.

### `wa status`

Non-blocking check of the daemon and WhatsApp connection state.

```
Usage:
  wa status
```

Output:
```
Connected as 5511999999999@s.whatsapp.net (device id 12)
Last event: 2026-04-11T17:00:00Z
```

JSON output includes `connected`, `jid`, `deviceId`, and `lastEvent`.

### `wa send`

Send a text message.

```
Usage:
  wa send --to <jid> --body <text>

Flags:
      --body string   message text (<= 64 KB)
      --to string     recipient JID (e.g. 5511999999999@s.whatsapp.net)
```

Blocked if the recipient JID is not in the allowlist with the `send` action. Blocked if the rate limiter or warmup ramp says no. There is no `--force`.

Exit codes: 0 ok, 11 not-allowlisted, 12 rate-limited, 10 daemon not running.

### `wa sendMedia`

Send an image, video, audio, or document.

```
Usage:
  wa sendMedia --to <jid> --path <file> [--caption <text>] [--mime <type>]

Flags:
      --caption string   optional caption
      --mime string      optional MIME type override
      --path string      path to media file on daemon's filesystem
      --to string        recipient JID
```

The `--path` is resolved **on the daemon's filesystem**, not the client's. For remote setups, stage the file into a location the daemon can read (e.g. `~/Library/Caches/wa/thumbnails/` or `$XDG_CACHE_HOME/wa/`).

Auto-detects MIME from the file extension; override with `--mime image/webp` for edge cases.

Message-body size limit is 16 MB for media per WhatsApp's server rules.

### `wa markRead`

Mark a specific message as read.

```
Usage:
  wa markRead --chat <jid> --messageId <id>
```

Requires the `read` action on the chat JID. No-op if the recipient has "Read receipts" disabled.

### `wa react`

React to a message with an emoji. Empty emoji removes the reaction.

```
Usage:
  wa react --chat <jid> --messageId <id> --emoji <emoji>
```

Example: `wa react --chat 5511999999999@s.whatsapp.net --messageId ABCD1234 --emoji 👍`

### `wa groups`

List joined groups.

```
Usage:
  wa groups
```

Output is a table of `JID | SUBJECT | PARTICIPANT_COUNT`. JSON mode returns a full participant list per group.

### `wa allow`

Manage the per-profile JID allowlist. Default-deny — a JID not in the list cannot receive messages from you via `wa`.

```
wa allow list
wa allow add <jid> --actions send,read,group.add,group.create
wa allow remove <jid>
```

**Actions**:

| Action | Semantics |
|---|---|
| `send` | Outbound `wa send`/`wa sendMedia`/`wa react` permitted |
| `read` | `wa markRead` permitted |
| `group.add` | Permitted to add this JID to groups |
| `group.create` | Permitted to create groups containing this JID |

Hot-reloaded on `SIGHUP` to `wad` or via the `allow` RPC. Persisted to `$XDG_CONFIG_HOME/wa/<profile>/allowlist.toml`.

### `wa wait`

Block until a matching event arrives from the daemon. Useful for scripts that want to react to inbound traffic.

```
Usage:
  wa wait --events <types> [--timeout <dur>]

Flags:
      --events string      comma-separated event types (e.g. message,receipt)
      --timeout duration   maximum time to wait (default 30s)
```

Exits 0 on first match, 12 on timeout. Emits one JSON object to stdout.

### `wa panic`

**Destructive**: unlinks the device server-side via WhatsApp's admin API AND wipes the local session database. The next `wa pair` will start fresh. Intended for "lost laptop" or "compromised session" scenarios.

```
Usage:
  wa panic
```

No flags. Always prompts for confirmation unless `--json` is set (in which case it assumes a tooling invocation and proceeds).

### `wa profile`

Manage per-profile state.

```
wa profile list            # table of all profiles
wa profile use <name>      # set active profile (atomic tempfile-rename)
wa profile create <name>   # mkdir + seed empty allowlist (does NOT pair)
wa profile rm <name>       # remove; --yes skips confirmation prompt
wa profile show [name]     # metadata (defaults to active profile)
```

Hard constraints on `rm`:
1. Cannot remove the active profile (switch first)
2. Cannot remove the only profile
3. Cannot remove a profile whose daemon is running (stop first)

Collision check: if you try `wa profile create work` and `Work/` already exists (APFS/HFS+ case folding), the command refuses.

### `wa migrate`

Migrate a pre-008 single-profile install to the 008 per-profile layout. Normally runs automatically on first 008 `wad` startup; this subcommand exposes it explicitly.

```
wa migrate                  # apply the migration
wa migrate --dry-run        # print planned moves without acting
wa migrate --rollback       # reverse a completed migration (strict pre-conditions)
```

Crash-safe: uses a `.migrating` write-ahead marker and a single `os.Rename` pivot. The migration uses a 25-step crash-safe sequence.

### `wa completion`

Generate shell completion scripts.

```
wa completion bash > /tmp/wa.bash && source /tmp/wa.bash
wa completion zsh  > ~/.zsh/completions/_wa
wa completion fish > ~/.config/fish/completions/wa.fish
```

Profile names complete dynamically via `filepath.Glob($XDG_DATA_HOME/wa/*/session.db)`.

### `wa version`

Print the CLI version, git commit, and build date.

### `wa upgrade`

Print the upgrade command for your install method (Homebrew, Nix, go install, or tarball). Does not actually perform the upgrade — that requires elevated privileges and your install-method-specific tooling.

---

## `wad` daemon commands

### `wad` (no subcommand)

Run the daemon in the foreground. Use `--profile <name>` to run a non-default profile. Use `--log-level debug` for verbose output. Signal-handling: `SIGINT`/`SIGTERM` → graceful shutdown, `SIGHUP` → reload allowlist.

### `wad install-service`

Install the platform-specific service unit for the given profile.

```
wad install-service --profile default
wad install-service --profile work
wad install-service --dry-run          # print generated unit file, do not install
```

- **Linux**: writes `~/.config/systemd/user/wad@.service` (template, once) and runs `systemctl --user enable --now wad@<profile>.service`. Hints that `loginctl enable-linger $USER` is required for headless operation.
- **macOS**: writes `~/Library/LaunchAgents/com.yolo-labz.wad.<profile>.plist` and runs `launchctl bootstrap gui/$(id -u) <plist>`.

Refuses to run as root (constitution §III).

### `wad uninstall-service`

```
wad uninstall-service --profile work
```

Removes only the specified profile's unit/plist. Other profiles are untouched.

### `wad migrate`

Internal handler for the `wa migrate` client command. Accepts `--dry-run`, `--rollback`, `--profile`.

---

## 7. Output schemas (NDJSON)

With `--json`, every `wa` subcommand emits newline-delimited JSON objects. Each object carries a `schema` field so Claude Code plugins (and other consumers) can dispatch on stable schemas without brittle field sniffing.

```json
{"schema":"wa.status/v1","connected":true,"jid":"5511999999999@s.whatsapp.net","deviceId":12,"lastEvent":"2026-04-11T17:00:00Z"}
{"schema":"wa.send.result/v1","messageId":"ABCD1234","timestamp":1713888000,"to":"5511999999999@s.whatsapp.net"}
{"schema":"wa.event/v1","type":"message","chat":"5511999999999@s.whatsapp.net","sender":"5511999999999@s.whatsapp.net","ts":1713888010,"body":"..."}
{"schema":"wa.error/v1","code":-32012,"message":"not allowlisted","jid":"..."}
```

Schema versions use `<name>/v<N>` semantics. A bump (`v1` → `v2`) is a breaking change and only happens in a major release.

---

## 8. Exit codes

Following `sysexits.h`:

| Code | Name | Meaning |
|---|---|---|
| 0 | OK | Success |
| 1 | Generic | Unexpected runtime error |
| 10 | Unavailable | Daemon not running or flock held |
| 11 | Not allowlisted | Recipient JID not in allowlist for the requested action |
| 12 | Rate limited | Per-second / per-minute / per-day cap exceeded, or warmup ramp not reached |
| 64 | Usage | Bad flags, bad JID, or invalid profile name |
| 78 | Config | Bad config file, migration pre-flight failed, multi-profile ambiguity |

---

## 9. Filesystem layout

The canonical filesystem layout is documented below.

Summary:

```
$XDG_DATA_HOME/wa/<profile>/session.db        session + ratchets (0600)
$XDG_DATA_HOME/wa/<profile>/messages.db       history (0600)
$XDG_CONFIG_HOME/wa/<profile>/allowlist.toml  per-profile allowlist (0600)
$XDG_CONFIG_HOME/wa/active-profile            pointer to current profile
$XDG_CONFIG_HOME/wa/.schema-version           layout version (2 = feature 008)
$XDG_STATE_HOME/wa/<profile>/audit.log        append-only audit log (0600)
$XDG_STATE_HOME/wa/<profile>/wad.log          daemon log
$XDG_RUNTIME_DIR/wa/<profile>.sock            unix socket (0600)
$XDG_RUNTIME_DIR/wa/<profile>.lock            single-instance flock
$XDG_CACHE_HOME/wa/                           media thumbnails (shared across profiles)
```

On macOS: socket lives under `~/Library/Caches/wa/`, data/state under `~/Library/Application Support/wa/`. See the platform-specific rows in the contracts document.

All per-profile directories are mode `0700`, all files `0600`. `wa` refuses to operate on a socket directory that isn't mode `0700` and owned by `geteuid()` (FR-042).

---

## 10. Allowlist and rate limiter

**Allowlist** is default-deny. JIDs you have not explicitly added cannot receive messages, receive group adds, or be marked as read by `wa`. Enforced inside `wad`, hot-reloaded on file change.

**Rate limiter** is hardcoded and non-overridable:

| Dimension | Limit |
|---|---|
| Per second | 2 sends |
| Per minute | 30 sends |
| Per day | 1,000 sends |
| Group creations | 5/day |
| Participant adds | 50/day |
| Broadcast lists | forbidden |

**Warmup** for fresh sessions auto-scales:

| Days since pairing | Effective caps |
|---|---|
| 0–7 | 25 % |
| 8–14 | 50 % |
| 15+ | 100 % |

The warmup timestamp is sourced from the persisted session creation time (FR-032); daemon restarts do NOT reset it.

There is no `--force` flag and there will not be one (constitution §III).

---

## 11. Audit log

Every mutating call writes an append-only JSON Lines entry to `$XDG_STATE_HOME/wa/<profile>/audit.log`. Example:

```json
{"ts":"2026-04-11T17:00:00Z","actor":"wad:default","action":"send","subject":"5511999999999@s.whatsapp.net","decision":"ok","detail":""}
{"ts":"2026-04-11T17:00:05Z","actor":"wad:default","action":"grant","subject":"5511888888888@s.whatsapp.net","decision":"ok","detail":"actions=send,read"}
{"ts":"2026-04-11T17:00:10Z","actor":"wad:default","action":"send","subject":"5511777777777@s.whatsapp.net","decision":"denied","detail":"not allowlisted"}
{"ts":"2026-04-11T17:00:15Z","actor":"wad:migrate","action":"migrate","subject":"","decision":"ok","detail":"legacy single-profile → default/ (schema v1 → v2)"}
```

The audit log is **never auto-rotated**. Back it up as part of your regular backup strategy. `wa panic` leaves the audit log intact.

The `Actor` field format is `wad:<profile>` so entries from concurrent profiles are unambiguous when side-by-side logs are compared.

---

## 12. Service installation

See [§3 First run](#3-first-run--pairing) for `wad install-service`. Per-platform notes:

### Linux (systemd user unit)

Installed as a **template unit** `wad@.service` at `~/.config/systemd/user/`. Each profile gets its own instance:

```bash
wad install-service --profile default
wad install-service --profile work
systemctl --user list-units 'wad@*'
```

Hardening directives that actually work in user mode: `NoNewPrivileges`, `LockPersonality`, `RestrictRealtime`, `RestrictSUIDSGID`, `SystemCallFilter=@system-service`, `SystemCallArchitectures=native`, `Restart=on-failure`, `RestartSec=5s`.

`MemoryDenyWriteExecute` is **deliberately absent** — Go's garbage collector is incompatible with it (systemd#3814).

Mount-namespace directives (`ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `RestrictNamespaces`) are **deliberately absent** from the user template — they no-op or fail in user mode. The NixOS system module (§12.3) gets the full set.

For headless operation:
```bash
loginctl enable-linger $USER
```

### macOS (launchd)

One plist per profile at `~/Library/LaunchAgents/com.yolo-labz.wad.<profile>.plist`. Loaded via `launchctl bootstrap gui/$(id -u) <plist>` (2.0 syntax).

Key directives:
- `KeepAlive` as a dict `{Crashed: true, SuccessfulExit: false}` — a clean `wa panic` does NOT respawn
- `ProcessType = Background` — throttled CPU/IO
- `EnvironmentVariables.PATH` set explicitly (launchd empties PATH for children)
- `LimitLoadToSessionType` deliberately absent so SSH-session invocations work

Uninstall:
```bash
wad uninstall-service --profile work
```

### NixOS (`services.wa.*`)

The system module (`flake.nixosModules.default`) installs wad as a system-level systemd service with the **full** hardening set (since system-mode systemd can set up mount namespaces). See `nix/nixos-module.nix`.

```nix
services.wa = {
  enable = true;
  profile = "default";
  user = "wa";
  logLevel = "info";
};
```

### home-manager

For per-user NixOS/macOS deployments, use `homeManagerModules.default` which mirrors the user-mode systemd template unit:

```nix
services.wa = {
  enable = true;
  profile = "default";
  autoStart = true;
};
```

Remember to run `loginctl enable-linger $USER` yourself — home-manager does not manage linger.

---

## 13. Migration and backups

### Automatic migration (007 → 008)

First run of an 008-or-newer `wad` binary on a 007-format install triggers the migration transaction. See §Concepts/Multi-profile.

The migration:
1. Checkpoints every SQLite WAL file (`PRAGMA wal_checkpoint(TRUNCATE)`)
2. Writes a `.migrating` marker listing every planned move, fsynced
3. Uses `os.Rename` (metadata-only) to move files into `default/` subdirectories
4. Fsyncs the pivot parent directory
5. Writes `schema-version=2` and `active-profile=default` via atomic tempfile-rename
6. Appends one `migrate` audit entry
7. Deletes the `.migrating` marker

Crash at any step: next startup reads the marker and either completes forward or rolls back. Covered by a subprocess `SIGKILL` injection test (SC-013).

Rollback:
```bash
wa migrate --rollback
```

Pre-conditions: schema version is 2, only the `default` profile exists, no marker, no running daemon.

### Manual backups

Since the session DB is plaintext, the filesystem layout is simple enough to back up with any standard tool:

```bash
# rsync backup (per profile)
rsync -a --delete \
  "$XDG_DATA_HOME/wa/default/" \
  "$XDG_CONFIG_HOME/wa/default/" \
  "$XDG_STATE_HOME/wa/default/" \
  backups/wa-default/
```

Encrypt at rest via FileVault (macOS), LUKS (Linux), or dm-crypt. SQLCipher is rejected because it requires CGO.

---

## 14. Troubleshooting

### Daemon not running

```
Error: dial unix /Users/you/Library/Caches/wa/default.sock: connect: no such file or directory
```

`wad` isn't running. Start it: `wad --profile default` or start the service.

### Wrong profile

```
Error: multiple profiles exist (default, work); pass --profile or run 'wa profile use <name>'
```

Set one explicitly: `wa --profile work status` or `wa profile use work`.

### Rate limited

```
Error (exit 12): rate limited — try again in 3s
```

Wait. There is no `--force`. The limiter is measuring you against WhatsApp anti-abuse thresholds; bypassing it risks a ban.

### Not allowlisted

```
Error (exit 11): 5511999999999@s.whatsapp.net is not allowlisted for action 'send'
```

Add it: `wa allow add 5511999999999@s.whatsapp.net --actions send`.

### macOS socket path too long

```
Error (exit 78): socket path length 107 > 104 (sun_path budget on darwin)
```

Your home directory path is longer than 32 bytes and the resulting `~/Library/Caches/wa/<profile>.sock` overflows `sun_path`. Use a shorter profile name (e.g. `w` instead of `work-account-42`).

### Apple Sandbox client

A `wa` CLI invocation from inside an App Sandbox container cannot connect to the socket regardless of permissions. Documented non-goal — run from an unsandboxed terminal.

### Need more logging

```bash
WA_LOG_LEVEL=debug wad --profile default
# OR for a running service
journalctl --user -u wad@default -f          # Linux
tail -f ~/Library/Logs/wad-default.log       # macOS (if StandardOutPath points there)
```

### Tests fail under `-race` on my machine

There is a known intermittent flake in `TestSubscribe_BackpressureClose` and `TestShutdown_CleanShutdownCompletesQuickly` (feature 004 sockettest). Retry the run; not related to anything in your local changes. Fix is tracked as a follow-up.

---

## See also

- [`README.md`](../README.md) — project overview and quickstart
- [`SECURITY.md`](../SECURITY.md) — full threat model
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — speckit workflow + commit style
