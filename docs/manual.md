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
go install github.com/yolo-labz/wa/v2/cmd/wa@v2.0.14
go install github.com/yolo-labz/wa/v2/cmd/wad@v2.0.14
```

The module path includes `/v2` per Go's [semantic import versioning](https://research.swtch.com/vgo-import) rule. `go install` builds without ldflags, but `wa version` falls back to `runtime/debug.BuildInfo.Main.Version` (Go 1.18+), which records the module version recorded by `go install`. So `wa version` reports `v2.0.14` correctly. The GoReleaser and Nix paths additionally inject the tag via ldflags + commit + date.

### Verifying the install

```bash
wa version
wad --help
ls -la "$XDG_RUNTIME_DIR/wa/" 2>/dev/null || echo "no sockets yet (daemon not started)"
```

### Docker

The repo ships a distroless multi-stage `Dockerfile` (~12 MB image,
nonroot, reproducible flags) and a `docker-compose.yaml` quickstart:

```bash
docker compose up -d
docker compose exec wa /usr/local/bin/wa --socket /data/run/wa/default.sock pair
docker compose exec wa /usr/local/bin/wa --socket /data/run/wa/default.sock \
  allow add 5511999999999 --actions send
```

All state lives on the `/data` volume (XDG paths are pre-set in the
image) — protect it; losing it means re-pairing. Expose REST + MCP by
setting `WAD_REST_HTTP_ADDR` + `WAD_REST_TOKEN` (see compose comments).

### install.sh

`install.sh` (repo root) downloads the latest GoReleaser release for
your OS/arch, verifies its SHA-256 against the published
`checksums.txt`, and installs to `~/.local/bin` (override:
`WA_INSTALL_DIR`). It is deliberately short — read it first:

```bash
curl -fsSL https://raw.githubusercontent.com/yolo-labz/wa/main/install.sh -o install.sh
less install.sh && bash install.sh
```

For provenance beyond checksums, releases carry GitHub attestations:
`gh attestation verify wa_linux_amd64.tar.gz --repo yolo-labz/wa`.

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

`STATUS` is recomputed on every `list` by dialing each profile's socket, so
it reflects live connectivity:

| STATUS | Meaning |
|---|---|
| `connected` | Socket exists and a daemon accepted the connection |
| `socket-refused` | Socket file exists but nothing is listening (crashed daemon left a stale socket) |
| `daemon-stopped` | No socket file — daemon not running |
| `not-paired` | No `session.db` — profile created but never paired |

`LAST_SEEN` is the socket file's mtime — the last time the daemon touched it,
**not** "now". A `connected` row with an old `LAST_SEEN` is normal for an idle
session.

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

> **Recipient alias.** The flag naming the target chat is spelled `--to`,
> `--jid`, or `--group` depending on the command. `--chat` is accepted as a
> universal synonym on every one of these commands, so you can use `--chat
> <jid>` anywhere a recipient is expected (e.g. `wa send --chat …`, `wa
> contact block --chat …`, `wa group add --chat …`). The original flags
> still work unchanged.

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
  wa send --to <jid> --body <text> [--mention <jid-or-number> ...]

Flags:
      --body string       message text (<= 64 KB)
      --mention strings   @mention a member (repeatable, --body sends only); bare number or JID
      --to string         recipient JID (e.g. 5511999999999@s.whatsapp.net)
```

Blocked if the recipient JID is not in the allowlist with the `send` action. Blocked if the rate limiter or warmup ramp says no. There is no `--force`.

**@mentions.** `--mention` is repeatable and accepts a bare number (`5581999999999`) or a full JID; the daemon normalises each to `<number>@s.whatsapp.net`. A message with mentions is sent as an `ExtendedTextMessage` carrying `ContextInfo.MentionedJID`, which is what makes the recipient's client render a **tappable, notifying** mention. WhatsApp only renders a mention where the body contains the matching `@<number>` token, so the daemon **appends `@<number>` to `--body` for any mentioned identity whose token is not already there** — write the token yourself to place it inline, or omit it and let the daemon append it at the end. Only addressable identities (user / LID / hosted / bot) can be mentioned; a group or channel JID is rejected. Mentions apply to `--body` sends only (not the interactive reply modes).

```
# Tag a group member inline (token written in the body):
wa send --to 120363000000000000@g.us --body "oi @5581999999999 tudo bem?" --mention 5581999999999

# Or omit the token and let the daemon append it:
wa send --to 120363000000000000@g.us --body "bom dia" --mention 5581999999999
```

Exit codes: 0 ok, 11 not-allowlisted, 12 rate-limited, 10 daemon not running.

### `wa sendMedia`

Send an image, video, audio, or document.

```
Usage:
  wa sendMedia --to <jid> --path <file> [--caption <text>] [--mime <type>] [--filename <name>]

Flags:
      --caption string    optional caption
      --filename string   display filename for document sends (defaults to --path basename)
      --mime string       optional MIME type override
      --path string       path to media file on daemon's filesystem
      --to string         recipient JID
```

In **local/socket** mode the `--path` is resolved **on the daemon's filesystem**, not the client's. In **`--remote`** mode the path is read from the **client** filesystem: the bytes are transparently uploaded to the daemon's content-addressed store (`POST /media/upload`, 16 MiB cap, `send` token scope) and the message is then sent by the resulting sha256 — the display filename travels along automatically, no manual staging needed. Use `wa push` first if you want to upload once and reuse the hash (pass `--filename` so the recipient sees a named, openable document instead of a generic attachment).

Without `--mime`, the daemon resolves the type from the filename extension first, then the store's content-sniffed type (sha256 sends), then the payload's magic bytes; an explicit `--mime image/webp` always wins. The resolved type also picks the WhatsApp category (image/video/audio/document), so a PNG sent without flags arrives as a real image, not a document.

Message-body size limit is 16 MB for media per WhatsApp's server rules.

### `wa markRead`

Mark a specific message as read.

```
Usage:
  wa markRead --chat <jid> --messageId <id>
```

Requires the `read` action on the chat JID. No-op if the recipient has "Read receipts" disabled.

### `wa sendSeen`

Mark a chat read ("seen") at its **newest incoming message** — no message id needed. The daemon looks the anchor up in its local history and sends the read receipt there; the result echoes which `messageId` it used.

```
Usage:
  wa sendSeen --chat <jid>
```

Use `markRead` instead when you must anchor an explicit message id. Requires the `read` action on the chat JID; fails with `-32117 message_not_found` when the daemon has no incoming message on record for that chat (e.g. history not yet synced).

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

The remaining subcommands are grouped by purpose below. Every entry's flag set is cross-checked against the live `wa <cmd> --help`. Most operate against an already-paired daemon over the unix socket (or `--remote`).

**Messaging extras**

### `wa reply`

Send a quoted reply that visually threads under an existing message.

```
Usage:
  wa reply --to <jid> --quoted-id <id> --body <text>

Flags:
      --body string              reply text
      --idempotency-key string   FR-034a replay key
      --quoted-id string         message ID being quoted
      --to string                recipient JID
```

All three of `--to`, `--quoted-id`, `--body` are required. Same allowlist + rate-limiter gating as `wa send`. Supplying `--idempotency-key` makes a retry safe: the same key + identical params replays the cached result; the same key + different params returns `-32101`.

### `wa msg`

Moderate an already-sent message. Parent for `revoke`, `edit`, `forward`, `star`, and `disappearing`.

```
wa msg revoke --chat <jid> --messageId <id> [--scope self|everyone]
wa msg edit --chat <jid> --messageId <id> --body <text>
wa msg forward --to <jid> --sourceChat <jid> --messageId <id>
wa msg star --chat <jid> --messageId <id> [--unstar]
wa msg disappearing --chat <jid> --seconds off|24h|7d|90d
```

- **revoke** — `--scope everyone` (default) emits a REVOKE so peers delete their copy; `--scope self` wipes only the local row.
- **edit** — replaces the body; refused with `-32100 policy_refused` if the original is older than 15 minutes.
- **forward** — re-sends with the "Forwarded" chip; the allowlist + rate limiter apply to the destination chat, not the source.
- **star** — marks the message in the starred folder; `--unstar` removes it. Idempotent.
- **disappearing** — sets the chat-level timer to one of `off|24h|7d|90d` (or the raw seconds `0`, `86400`, `604800`, `7776000`); any other value returns `-32602`.

Every `wa msg` subcommand accepts `--idempotency-key`.

### `wa poll`

Interact with polls. The only verb is `vote`.

```
Usage:
  wa poll vote --chat <jid> --poll-id <id> --option <n> [--option <n> ...]

Flags:
      --chat string      chat JID the poll lives in (required)
      --option ints      zero-based option index (repeatable)
      --poll-id string   poll message ID (required)
```

Repeat `--option` for multi-select (`--option 0 --option 2`). Passing no `--option` clears the vote. At the v2.0.x whatsmeow pin the adapter returns `-32000 upstream_error` for any well-formed call; shape errors come back as `-32602` (exit 64).

**Chat operations**

### `wa chat`

Manage chat-level state. Parent for the read view (`list`, `last-active`) and the state mutators (`archive`, `pin`, `mute`, `mark-unread`).

```
wa chat list [--limit 200]            # chats, most-recently-active first
wa chat last-active [--limit 10]      # the recently-touched subset
wa chat archive --chat <jid> [--unarchive]
wa chat pin --chat <jid> [--unpin]
wa chat mute --chat <jid> (--until <RFC3339> | --duration <dur> | --unmute)
wa chat mark-unread --chat <jid>
```

`list`/`last-active` are read-only views (`LAST | MSGS | KIND | NAME | JID`). WhatsApp caps pinned chats at 3 — the 4th `pin` returns `-32000`. A past `--until` on `mute` returns `-32100 policy_refused`. All mutators are server-side idempotent and accept `--idempotency-key`.

### `wa contact`

Manage the **server-side** blocklist and resolve PN ↔ LID identities. Parent for `block`, `unblock`, `blocklist`, `lid`, `pn`.

```
wa contact block --jid <jid>
wa contact unblock --jid <jid>
wa contact blocklist                  # live server-side list
wa contact lid <pn>                   # phone-number JID → LID
wa contact pn <lid>                   # LID → phone-number JID
```

`block` refuses future send / sendMedia / react / reply to the target at the socket boundary (`-32100`) and writes an audit entry. `blocklist` always reads live from the server, so out-of-band unblocks from the phone are reflected. `lid`/`pn` exit 0 with empty stdout when no mapping is known yet (not an error). `block`/`unblock` accept `--idempotency-key`.

**Groups**

### `wa group`

Administer groups (distinct from `wa groups`, which only lists). Parent for `create`, `leave`, `add`, `remove`, `promote`, `demote`, `edit`, and the `invite` sub-tree.

```
wa group create --subject <s> --participant <jid> [--participant <jid> ...]
wa group leave --group <jid>
wa group add --group <jid> --participant <jid> [...]
wa group remove --group <jid> --participant <jid> [...]
wa group promote --group <jid> --participant <jid> [...]
wa group demote --group <jid> --participant <jid> [...]
wa group edit --group <jid> [--subject <s>] [--description <d>] [--icon-path <jpeg> | --remove-icon]
wa group invite get --group <jid>
wa group invite revoke --group <jid>
wa group invite join --url https://chat.whatsapp.com/<code>
```

Adapter caps: subject ≤ 25 bytes, ≤ 5 group-creates/day, ≤ 50 participant-adds/day. `edit` applies fields in order (subject → description → icon). `invite join` requires a URL matching `^https://chat.whatsapp.com/[A-Za-z0-9]+$`; revoked or invalid links return `-32000`. Every mutating subcommand accepts `--idempotency-key`. Creating a group and adding members is gated by the allowlist `group.create` / `group.add` actions (see §10).

**Discovery and inspection**

### `wa history`

Show message history for one chat (newest-first table, or NDJSON with `--json`).

```
Usage:
  wa history --chat <jid> [--limit 50] [--before <messageId>]

Flags:
      --before string   cursor: message ID to paginate from
      --chat string     chat JID
      --limit int       max messages to return (default 50)
```

`--before` is the pagination cursor — pass the oldest message ID from the previous page to walk further back.

### `wa messages`

List recent messages across **all** chats. The bare command dumps the most-recent rows; the `list` subcommand applies filters.

```
wa messages [--limit 50]
wa messages list [--chat <jid>] [--media-type audio|video|image|pdf|<mime>] \
                 [--from-me | --from-me=false] [--since <RFC3339>] [--until <RFC3339>] [--limit 50]
```

`--from-me` is tri-state: omit it to list both directions, `--from-me` for outbound only, `--from-me=false` for inbound only. `--limit` on `list` caps at 500.

### `wa search`

Full-text (FTS5) search across all stored messages.

```
Usage:
  wa search --query <fts5> [--limit 20]

Flags:
      --limit int      max results (default 20)
      --query string   FTS5 search query
```

Renders the same message table as `wa history`; add `--json` for NDJSON.

### `wa thread`

Fetch a paginated window of messages for a chat by cursor. Parent for `get`.

```
Usage:
  wa thread get --chat <jid> [--cursor <c>] [--limit 50]

Flags:
      --chat string     chat JID
      --cursor string   pagination cursor
      --limit int       page size (default 50, ≤200)
```

Lower-level than `wa history`/`wa messages` — intended for clients that page through a thread with an opaque cursor.

### `wa export`

Export one chat's messages **oldest-first** as NDJSON, for archival or piping to `jq`.

```
Usage:
  wa export --chat <jid> [--since <RFC3339>] [--until <RFC3339>] [--limit 0]

Flags:
      --chat string    chat JID to export
      --limit int      max messages (0 = all, ≤100000)
      --since string   lower time bound (RFC3339)
      --until string   upper time bound (RFC3339)
```

A zero-row export exits **64** with a stderr hint (a chat with no rows is indistinguishable from a typo'd JID), so callers brute-forcing JID variants can tell a miss from a hit. stdout stays clean for the pipe.

### `wa purge`

**Destructive**: delete every stored message for a chat from the local database.

```
Usage:
  wa purge --chat <jid> --yes

Flags:
      --chat string   chat JID to purge
  -y, --yes           confirm deletion (required)
```

Without `--yes` the command is a dry run: it prints `This will delete all messages for <jid>. Use --yes to confirm.` to stderr and deletes nothing. With `--yes` it removes the rows and reports the count (`Purged N messages from <jid>`). This touches only the local store — it does not revoke or delete messages on peers' devices (use `wa msg revoke` for that).

### `wa contacts`

Contact-directory operations over the local mirror. Parent for `lookup`, `search`, `list`, `annotate`, `sync`.

```
wa contacts lookup --jid <jid>
wa contacts search --query <q> [--limit 20]      # trigram search, ≤50
wa contacts list [--limit 100]                   # most-recently-changed first, ≤500
wa contacts annotate --jid <jid> [--notes <text>] [--tag <t> --tag <t> ...]
wa contacts sync [--mode delta|full]             # default: delta
```

`annotate` attaches free-text notes and repeatable tags. `sync` triggers a contact mirror pull (`delta` by default, `full` for a complete re-sync).

### `wa media`

Content-addressed media operations over the on-disk cache. Parent for `list`, `resolve`, `download`, `fetch`, `gc`.

```
wa media list [--chat <jid>] [--media-type audio|video|image|pdf|<mime>] [--limit 50]
wa media resolve --sha256 <64-hex>               # cached path for a content hash
wa media download --message-id <id> [--transcribe]   # lazy-fetch payload; prints on-disk path
wa media fetch (--sha256 <hex> | --message-id <id>) [--out <file>]   # bytes to file/stdout
wa media gc [--older-than-seconds N] [--dry-run]
```

`list` shows per-object cache status (sha256, size, duration; `--limit` ≤500). `download --transcribe` runs voice-note transcription. `gc` deletes cached blobs older than the cutoff (default 30 days); `--dry-run` reports candidate count + reclaimable bytes on stderr without deleting.

### `wa push`

Upload a **client-local** file to a `--remote` daemon's content-addressed media store and print the resulting sha256. Stage a file once, then reference the hash from `sendMedia --sha256` (or `media fetch`) without re-transferring the bytes.

```
wa --remote https://wa.example.com push ./poster.png            # prints <sha256>
wa --remote https://wa.example.com sendMedia --to <jid> --sha256 <sha256> --filename poster.png
```

The store is content-addressed, so the hash carries no filename — pass `--filename` on the send so document recipients see a named, openable file (the daemon falls back to its content-sniffed type for the MIME either way).

`--remote`-only (in local mode the daemon already reads your filesystem, so there is nothing to upload). The body is capped at 16 MiB and the token (from `$WA_TOKEN`, never a flag) needs the `send` scope. `--json` emits a `wa.media.upload/v1` envelope (`schema`, `sha256`, `size`).

### `wa doctor`

Run 11 self-diagnostic checks against the local `wad` install (socket reachability, schema version, disk perms, clock skew, …).

```
Usage:
  wa doctor
```

Each check prints `[OK|WARN|FAIL] <name>` with a remediation hint on non-OK. `--json` emits a `wa.doctor/v1` envelope with the full check array.

### `wa health`

Non-blocking liveness probe — a lighter cousin of `wa status`.

```
Usage:
  wa health
```

Prints `profile`, `paired`, `connected`, `last-event`, and `session` start time. `--json` emits the raw `health` result. Suitable for cron/monitoring (`gatus`-style HTTP-less checks).

### `wa debug`

Daemon diagnostic helpers. Parent for `pprof`.

```
Usage:
  wa debug pprof [cpu|heap|goroutine|block|mutex] [--seconds N]
```

Captures a runtime profile from `wad`, writes it to a temp `*.pb.gz`, and opens it in `go tool pprof` when `go` is on PATH (otherwise prints the path). `--seconds` sets the CPU profiling window (cpu only; default 30s).

### `wa audit`

Inspect the daemon's tamper-evident audit log. Parent for `verify`.

```
Usage:
  wa audit verify [--path <audit.log>] [--key <keyfile>]
```

`verify` walks the audit log computing the HMAC chain and exits 0 only if every line verifies (non-zero on the first mismatch, with the offending line number). `--path` defaults to `$XDG_STATE_HOME/wa/<profile>/audit.log`; `--key` defaults to `<path>.key`. The key file is `0600` owned by the daemon UID — run `verify` as the daemon user or pass `--key` to a copy you own. See §11.

### `wa config`

Inspect resolved daemon configuration. Parent for `features`.

```
Usage:
  wa config features
```

`features` prints the resolved feature flags (`embeddings`, `scheduled_sends`, `labels`) as an on/off table, or as JSON with `--json`.

**Scheduling and drafts**

### `wa schedule`

Schedule future WhatsApp sends (state machine: pending → fired | cancelled | failed). Parent for `send`, `list`, `cancel`, `update`.

```
wa schedule send --to <jid> --fire-at <unix|RFC3339> [--body <text>] [--media <path>] \
                 [--kind send_text|send_media|create_draft] [--id <id>]
wa schedule list [--state pending|fired|cancelled|failed|''] [--limit 50]
wa schedule cancel --id <id>
wa schedule update --id <id> --fire-at <unix|RFC3339>
```

`--fire-at` accepts either unix seconds or an RFC3339 timestamp. `--kind` defaults to `send_text`; `create_draft` enqueues a draft for human review (see `wa draft`) instead of sending. `list --state ''` lists every state. `send` accepts `--idempotency-key`.

### `wa draft`

Human-review draft queue operations. Parent for `list`, `get`, `approve`, `reject`.

```
wa draft list [--state pending_review] [--limit 50]
wa draft get --id <id>
wa draft approve --id <id> [--decider cli|channel]
wa draft reject --id <id> [--decider cli|channel] [--reason <text>]
```

Drafts are messages staged for review (e.g. produced by `wa schedule send --kind create_draft` or an agent). `approve` releases the draft to the normal send path; `reject` discards it with an optional reason. `--decider` records who made the call (defaults to `cli`).

**Labels (WhatsApp Business)**

### `wa labels`

Manage WhatsApp Business labels. Behind the `labels` feature flag (`wa config features`). Parent for `list`, `create`, `delete`, `assign`, `unassign`.

```
wa labels list
wa labels create --name <name> [--color <0-19>]
wa labels delete --id <id>
wa labels assign --label-id <id> --chat <jid> [--message-id <id>]
wa labels unassign --label-id <id> --chat <jid> [--message-id <id>]
```

`assign`/`unassign` attach a label to a whole chat, or to a single message when `--message-id` is given. `--color` is a palette index in `[0,19]`. Requires a paired Business account; on a personal account the daemon returns an error.

**Privacy and identity**

### `wa privacy`

Read and change account privacy settings. Parent for `get`, `set`.

```
wa privacy get [--key groups|readReceipts|lastSeen|profile|about]
wa privacy set --key <k> --value everyone|contacts|nobody
```

`get` reads live from the server (out-of-band changes from the phone surface immediately) and prints one `key: value` line per dimension; `--key` filters to one. `set` changes a single dimension; both `--key` and `--value` are required and validated daemon-side (`-32602` on an unknown token).

### `wa session`

Manage the WhatsApp account session. Parent for `logout-all`.

```
Usage:
  wa session logout-all
```

`logout-all` unlinks **every** paired device from the account, not just this client. At the current whatsmeow pin it returns `-32000 upstream_error` (the upstream `LogoutAll` helper has not shipped); when it lands the command writes an audit entry. Accepts `--idempotency-key`. For wiping only this device's local session, use `wa panic`.

**Presence indicators**

### `wa presence`

Send typing / recording indicators. Parent for the `composing` and `recording` start/stop pairs.

```
wa presence composing start --chat <jid>
wa presence composing stop  --chat <jid>
wa presence recording start --chat <jid>
wa presence recording stop  --chat <jid>
```

Non-idempotent by design — excess calls are silently rate-limited by the adapter (1/s/chat); no idempotency key is accepted or forwarded.

**Live streaming and sync**

### `wa subscribe`

Stream events from the daemon as NDJSON until interrupted (the general-purpose form behind `wa stream` and `wa wait`).

```
Usage:
  wa subscribe --events <types> [--chats <jids>] [--senders <jids>] \
               [--not-senders <jids>] [--body-re <regex>] [--since <seq>]

Flags:
      --body-re string       body regex filter
      --chats string         comma-separated chat JIDs
      --events string        comma-separated event types (required)
      --not-senders string   comma-separated sender JIDs to exclude
      --senders string       comma-separated sender JIDs
      --since int            resume from this seq (Kafka-style cursor)
```

`--events` (e.g. `message,receipt`) is required. `--since` resumes from a sequence cursor so a reconnecting consumer does not miss events. Exits 0 on a clean subscription close or daemon shutdown; `12` on pong timeout.

### `wa stream`

Live-tail incoming messages as NDJSON — a convenience wrapper over `wa subscribe --events message`.

```
Usage:
  wa stream [--chat <jid>]

Flags:
      --chat string   only stream messages for this chat JID (comma-separated for multiple)
```

Prints one object per inbound message until ctrl-C. For receipts, status, or typing events — or to resume from a cursor — use `wa subscribe` directly.

### `wa sync`

Force and inspect on-demand history sync. Parent for `force`, `status`.

```
wa sync force [--chat <jid>] [--count N]
wa sync status
```

`force` requests recent history from WhatsApp now instead of waiting for the normal cadence — useful when messages are visible on your phone but the daemon's DB has not caught up. With `--chat` it blocks until that chat's pull lands (or ~30s elapses) and reports how many arrived; without `--chat` it fires a global newest-N pull (`--count` 1–50, default 50) and returns immediately. `status` shows the engine state (`syncing`, in-flight force pulls, worker queue depth).

**Embeddings (vector index)**

### `wa embeddings`

Inspect and manage the vector index. Requires the `embeddings` feature flag (`wa config features`). Parent for `status`, `purge`.

```
wa embeddings status
wa embeddings purge --yes
```

`status` reports embedder/index state (enabled, model, dimension, vector count). `purge` drops every vector from the index and requires `--yes` (`-y`) to confirm.

### `wa mcp serve` — Model Context Protocol (feature 111 M1)

```
wa mcp serve [--send-mode draft|direct|deny] [--toolsets messages,contacts,safety] [--read-only]
```

Serves MCP over stdio for agent runtimes (Claude Desktop/Code, Cursor,
VS Code). Every tool call is forwarded as JSON-RPC to the local `wad`
socket, so the allowlist, the non-overridable rate limiter, the audit
log, and the draft queue stay enforced in one place — below the agent.

Register the server in an MCP client as:

```json
{"mcpServers": {"wa": {"command": "wa", "args": ["mcp", "serve"]}}}
```

Send-mode (default **draft** — the safe default):

| Mode | `wa_send_message` / `wa_send_media` behaviour |
|---|---|
| `draft` | Files a human-review draft (audited `draft_create`, source `mcp`) and returns `draftId` + `pending_review`. Nothing is sent until you run `wa draft approve <id>` — the agent proposes, you dispose. |
| `direct` | Sends immediately; allowlist + rate limits still apply daemon-side. |
| `deny` | Send tools are not registered at all (read-only agent). |

M1 tools: `wa_send_message`, `wa_send_media`, `wa_schedule_message`,
`wa_search_messages`, `wa_get_thread`, `wa_wait_for_reply`,
`wa_transcribe_voice` (messages) · `wa_resolve_contact`, `wa_list_chats`
(contacts) · `wa_draft_review` (safety — deliberately read-only: an
agent can watch the queue but never approve its own sends).

Inbound message text in tool results arrives wrapped in the
`<channel source="wa">` envelope — agents must treat it as data, never
as instructions. Policy refusals surface as instructive tool errors
naming the operator remediation (e.g. `wa allow add <jid> --actions
send`).

#### Streamable HTTP transport (feature 111 M2)

When the REST listener is on (`WAD_REST_HTTP_ADDR`), the daemon also
serves MCP over **Streamable HTTP at `/mcp`**, behind the same bearer
auth — remote agent runtimes connect with
`{"type": "http", "url": "https://<host>/mcp", "headers": {"Authorization": "Bearer <token>"}}`.
Runs simultaneously with stdio, REST, SSE, and the socket.

Token scope filters tool REGISTRATION (spec 110d tokens): a `read`
token reaches a server with only the 9 read tools — send tools are
undiscoverable, not merely refused; `send`/`admin` tokens get the full
set under the configured send-mode. Env-token mode (`WAD_REST_TOKEN`)
implies admin. Env knobs: `WAD_MCP_SEND_MODE=draft|direct|deny`
(default `draft`), `WAD_MCP_DISABLE=1` (kill-switch — `/mcp` is never
registered).

M2 also adds the `groups` (`wa_group_info`) and `meta` (`wa_status`)
toolsets to both transports (12 tools total). Registry/.mcpb
distribution, resources and prompts land in M3 (spec 111).

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
{"schema":"wa.event/v1","type":"message","chat":"5511999999999@s.whatsapp.net","sender":"5511999999999@s.whatsapp.net","ts":1713888010,"kind":"text","channel":"<channel source=\"wa\" chat=\"5511999999999@s.whatsapp.net\" sender=\"5511999999999@s.whatsapp.net\" ts=\"1713888010\"><field name=\"body\">…</field></channel>"}
{"schema":"wa.error/v1","code":-32012,"message":"not allowlisted","jid":"..."}
```

Schema versions use `<name>/v<N>` semantics. A bump (`v1` → `v2`) is a breaking change and only happens in a major release.

On a `wa.event/v1` message event, `kind` names the message variant: `text`, `media` (image), `audio`, `video`, `document`, `sticker`, `contact`, `location`, `reaction`, `list_reply`, `button_reply`, `unknown`. Treat the list as open — a consumer MUST tolerate a kind it does not recognise, because WhatsApp keeps adding message types. Sender-authored text (body, caption, contact name, location name/address) is never a top-level field; it is always inside the `channel` envelope.

---

### `wa webhook` — signed outbound webhooks (feature 112)

```
wa webhook add https://n8n.example/webhook/abc --topics "message,receipt"
wa webhook list
wa webhook deliveries --state dead
wa webhook replay <delivery-id>
wa webhook rm <endpoint-id>
```

Every matching daemon event is POSTed to registered endpoints with
**Standard Webhooks** signing headers (`webhook-id`, `webhook-timestamp`,
`webhook-signature`; HMAC-SHA256) — verify with any standard-webhooks
library using the `whsec_…` secret printed ONCE by `add`. Payload is a
`wa.webhook/v1` envelope; message text stays inside the FR-005a
`<channel>` envelope. Delivery is DB-backed and restart-safe: 8
attempts over ~23 h (30 s → 12 h backoff), then `dead`; 5 consecutive
dead deliveries auto-disable the endpoint. Topics are event types
(`message`, `receipt`, `status`, `state.softStale`, …) or `*`.
On the REST surface, `webhook.add`/`webhook.remove` need an **admin**
token (endpoints are data-egress destinations); `list`/`deliveries`
are read-scope; `replay` is send-scope.

### Live events over SSE (`GET /v1/events`)

Server-Sent Events stream of the same subscriber-safe event projection
the socket `subscribe` method delivers (`message`, `receipt`, `status`,
`state.*`, `stream.drop`, …). Requires a read-scope token. Each frame is
`id: <seq>` / `event: <type>` / `data: <json>`, with a `: keepalive`
comment every 25 s.

An event kind the daemon has no subscriber projection for arrives as
type `unknown` with a payload of `{id, ts, goType}` only. That is
deliberate: the projection layer is where untrusted text gets folded
into the `<channel>` envelope, so a kind that skipped it ships its
structural minimum rather than its raw fields.

When the daemon's durable events ring (`events.db`) is healthy, frame
ids are the ring's monotonic seq and reconnects resume **gaplessly**:
send `Last-Event-ID: <seq>` (the standard `EventSource` reconnect
header) or `?since=<seq>` and the stream replays everything after that
cursor before going live. `?since=0` replays the whole retained ring
(last 10 000 events). A cursor that has fallen off the ring is
signalled with a synthetic `stream.drop` frame (no `id:` line) carrying
the gap size — reconcile from history, then continue. A fresh connection
without a cursor starts at "now".

`stream.drop` frames also arrive live, without any resume, when the
daemon's in-memory event buffer overflowed. Both sources share one
payload shape: `{gap, from, to, reason}` — `from`/`to` are the inclusive
sequence range you did not receive and `gap` is how many events that is.
The resume-side frame adds `dropped_total`, the ring's lifetime drop
counter.

```bash
curl -N -H "Authorization: Bearer $WA_TOKEN" \
  -H "Last-Event-ID: 41207" https://wa.example.com/v1/events
```

If `events.db` failed to open (degraded mode, visible in `wa health`),
the stream still works but is live-only and its ids are not resumable.

### Agent-readable surface (`/llms.txt`, `/v1/errors`, `/docs`)

Any daemon with the REST listener on also serves, **unauthenticated**:

- `GET /llms.txt` — agent-oriented summary of what wa is and how to
  integrate (mirrored at the repo root as `llms.txt`).
- `GET /openapi.json` — OpenAPI 3.1 contract for the transport
  endpoints; `GET /openrpc.json` — OpenRPC 1.3 catalog of the core
  JSON-RPC methods (drift-guarded: a documented method must exist on
  the dispatcher). `GET /docs` — interactive Scalar reference over the
  OpenAPI document (page loads Scalar from the jsdelivr CDN; the JSON
  contracts themselves are fully self-hosted).
- `GET /v1/errors` — the `wa.errors/v1` machine-readable catalog of
  every JSON-RPC error code with retryability + remediation (mirrored
  at `docs/errors.json`). Drift-guarded in CI: a new typed error cannot
  ship without a catalog row.

REST errors follow RFC 9457: any non-200 response (401, 403, 400, 413,
404, 500, 503) carries `Content-Type: application/problem+json` with
`type` (a URI reference into `/v1/errors#<name>` — resolve it against
the daemon host), `title` (the catalog message), `status` (HTTP echo),
optional `detail`, and `code` (the JSON-RPC error code as an extension
member). HTTP 200 on `/v1/rpc` always carries the JSON-RPC envelope —
dispatcher failures are application results and stay in the envelope's
`error` member, so integer-code error handling works unchanged across
both shapes.

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
