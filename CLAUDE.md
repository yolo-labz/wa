# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Greenfield. Zero code on disk as of 2026-04-06. This document is the architectural blueprint produced by a research swarm; it precedes the first commit. Treat every section below as a *decision already made* unless flagged **OPEN**.

## Mission

Build `wa`, a WhatsApp automation CLI that backs a Claude Code plugin turning a personal WhatsApp account into an AI-mediated personal assistant. Two binaries from one repo:

- **`wad`** — long-running daemon that owns the whatsmeow session, the SQLite ratchet store, and all WhatsApp I/O.
- **`wa`** — thin CLI client that speaks line-delimited JSON-RPC 2.0 to `wad` over a unix socket. This is what Claude Code's `Bash` tool actually invokes.

There is no MCP server in this repo by design — the user explicitly rejected MCP as bloat for the CLI/daemon. **This rule applies only to the `yolo-labz/wa` codebase.** The future `yolo-labz/wa-assistant` Claude Code plugin (separate repo) **must** use Anthropic's [Channels feature](https://docs.claude.com/en/docs/claude-code/channels), which is itself implemented as an MCP server — that is the only supported way to push events into a running Claude Code session. The plugin's MCP server is a thin Bun shim (~200–300 LoC, modeled on the official `external_plugins/telegram/server.ts`) that connects to `wad`'s unix socket and translates JSON-RPC events into `notifications/claude/channel`. It holds zero WhatsApp logic. See `specs/001-research-bootstrap/research.md` §OPEN-Q3 for the layering and the Telegram-plugin template.

## Decisions already locked in

| Area | Choice | Why |
|---|---|---|
| Language | **Go** (1.22+) | whatsmeow is the only production-grade WA library in 2026; no Rust/Python alternative exists |
| WA library | **`go.mau.fi/whatsmeow`** | MPL-2.0, Beeper-funded via Tulir, used by mautrix-whatsapp at six-figure scale |
| SQLite driver | **`modernc.org/sqlite`** | CGO-free → static cross-compile works |
| CLI framework | **`spf13/cobra` + `charmbracelet/fang` + `spf13/viper`** | cobra for ecosystem fit, fang for polish, viper for config layering |
| Paths | **`adrg/xdg`** | Honors XDG env vars on macOS unlike most libraries |
| Logging | **`log/slog` (stdlib) + `lmittmann/tint`** for dev | Structured by default, tinted in dev |
| Architecture | **Hexagonal / ports-and-adapters** | Five anticipated primary adapters (cli, socket, future REST, MCP, Channel) + one anticipated secondary swap (whatsmeow → Cloud API) puts us comfortably past the break-even point |
| IPC | **Line-delimited JSON-RPC 2.0 over unix socket** at `$XDG_RUNTIME_DIR/wa/wa.sock` (darwin fallback `~/Library/Caches/wa/wa.sock`) | Matches signal-cli; trivial Go impl; no protoc dependency |
| Supervisor | **launchd user agent** (darwin), **systemd user unit with `loginctl enable-linger`** (linux) | Never root |
| Distribution | **GoReleaser** → GitHub Releases (darwin-arm64, linux-amd64, linux-arm64) + Homebrew tap + Nix flake | Nix flake because the user runs nix-darwin |
| License | **Apache-2.0** | Matches the official Anthropic Telegram channel plugin precedent; explicit patent grant; MPL-2.0 file-level copyleft of whatsmeow upstream does NOT propagate to consumers (Mozilla MPL FAQ Q9–Q11). Resolved in `specs/001-research-bootstrap/research.md` §OPEN-Q5 — overturns prior MPL-2.0 default. |

## Repository layout

```
cmd/
  wa/main.go         # thin CLI client → unix socket
  wad/main.go        # daemon: composition root, wires everything
internal/
  domain/            # zero-dep entities + invariants
    jid.go contact.go group.go message.go allowlist.go session.go event.go
  app/               # use cases, depends only on domain + ports
    ports.go
    send_message.go list_groups.go stream_events.go pair_device.go
  adapters/
    primary/
      socket/        # JSON-RPC server, lives in wad
      rest/ mcp/ channel/    # future, all in wad, all reuse use cases
    secondary/
      whatsmeow/     # the real WA adapter (translates events/types at the boundary)
      sqlitestore/   # whatsmeow session persistence
      memory/        # in-memory fakes for tests + dry-run mode
      slogaudit/     # audit log adapter
.goreleaser.yaml
flake.nix
```

The cobra command tree lives **inside `cmd/wa`**, not under `internal/adapters/primary/cli/`. The CLI binary holds zero use case logic — every subcommand is a JSON-RPC call against `wad`. Hexagonal applies to `wad`; `wa` is a dumb client.

## Domain types

Pure Go, no whatsmeow imports. A `golangci-lint depguard` rule must enforce that no file under `internal/domain` or `internal/app` imports `go.mau.fi/whatsmeow/...`.

- **`JID`** — value object, parses/validates `user@s.whatsapp.net` and `id@g.us`. The single most important type; every leak of `whatsmeow/types.JID` into `app/` is a future migration tax.
- **`Contact`**, **`Group`**, **`Message`** (sum type: text/media/reaction), **`Conversation`** (deferred to v0.2).
- **`Allowlist`** — policy object with `Allows(jid JID, action Action) bool`. Belongs in domain because it is a business rule, not infrastructure.
- **`Session`** — opaque handle; contents live in the adapter.
- **Invariants in domain**: JID syntax, allowlist, message size (64KB text, 16MB media).
- **Invariants in adapters**: rate limiting, retry, encryption state, app-state sync, QR pairing.

## Ports (`internal/app/ports.go`)

Seven interfaces. Resist adding an eighth without a use case demanding it.

```go
type MessageSender interface {
    Send(ctx context.Context, to domain.JID, msg domain.Message) (domain.MessageID, error)
}

type EventStream interface {       // pull-based by design
    Next(ctx context.Context) (domain.Event, error)
    Ack(domain.EventID) error
}

type ContactDirectory interface {
    Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error)
    Resolve(ctx context.Context, phone string) (domain.JID, error)
}

type GroupManager interface {
    List(ctx context.Context) ([]domain.Group, error)
    Get(ctx context.Context, jid domain.JID) (domain.Group, error)
}

type SessionStore interface {
    Load(ctx context.Context) (domain.Session, error)
    Save(ctx context.Context, s domain.Session) error
    Clear(ctx context.Context) error
}

type Allowlist interface { Allows(domain.JID, domain.Action) bool }

type AuditLog interface {
    Record(ctx context.Context, e domain.AuditEvent) error
}
```

`EventStream` is **pull-based** even though whatsmeow's `AddEventHandler` is push: the secondary adapter runs a goroutine that funnels into a bounded buffer and exposes `Next`. This keeps backpressure and cancellation in the core's hands.

## Daemon, IPC, single-instance

- **Single instance** enforced by `flock(LOCK_EX|LOCK_NB)` on the SQLite store path *and* on the socket path. whatsmeow's `sqlstore` does **not** lock; two writers corrupt the ratchet store.
- **Pairing** is gated behind `wa pair`, which refuses to run if a session already exists. A second pair clobbers the device identity and the original session gets `StreamReplaced` from the server.
- **Reconnect** is delegated entirely to whatsmeow's built-in loop; the daemon surfaces `events.Disconnected/Connected` to subscribers so `wa status` shows red after laptop sleep.
- **Wire protocol sketch:**
  ```
  --> {"jsonrpc":"2.0","id":1,"method":"send","params":{"to":"…@s.whatsapp.net","body":"hi"}}
  <-- {"jsonrpc":"2.0","id":1,"result":{"messageId":"3EB0…","timestamp":1733000000}}
  --> {"jsonrpc":"2.0","id":2,"method":"subscribe","params":{"events":["message","receipt"]}}
  <-- {"jsonrpc":"2.0","method":"event","params":{"type":"message","from":"…","body":"…"}}
  ```
  One method per use case (`send`, `sendMedia`, `markRead`, `pair`, `status`, `groups`, `subscribe`, `wait`). Errors as JSON-RPC error objects with whatsmeow codes mapped 1:1.
- **Auth on the socket:** none beyond `0600` perms + `LOCAL_PEERCRED`/`SO_PEERCRED` UID check on accept. No tokens, no TLS — same-user-only by design.

## Safety (build the brakes first, not after the first ban)

Every one of these must exist before the first `Send` call leaves `wad`. WhatsApp bans aggressive automation in hours; retrofitting throttles after the architecture exists is painful.

1. **Allowlist, default-deny.** TOML at `$XDG_CONFIG_HOME/wa/allowlist.toml`, hot-reloaded on SIGHUP. Tiered actions: `read`, `send`, `group.add`, `group.create`. Edited via `wa allow add <jid> --actions send,read`. Per-action override via `wa grant --ttl 5m --actions group.add`.
2. **Rate limiter** as non-overridable middleware between use case and adapter. Per-second (1–2/s), per-minute (~30), per-day (~1000). No `--force` flag. Hard refusals: ≤5 group creations/day, ≤50 participant adds/day, no broadcast lists ever.
3. **Warmup** auto-engaged on a fresh session DB: 25 % caps for days 1–7, 50 % for days 8–14, 100 % thereafter.
4. **Audit log** at `$XDG_STATE_HOME/wa/audit.log`, append-only, never auto-rotated. Records every send and every authorization decision. Separate from the debug log.
5. **Inbound prompt-injection firewall.** All inbound message bodies must be wrapped in `<untrusted-sender>…</untrusted-sender>` before they reach Claude Code. Never inject inbound text into a system prompt.

## Filesystem layout (XDG)

| Purpose | Path (linux) | Path (darwin) |
|---|---|---|
| Session DB | `$XDG_DATA_HOME/wa/session.db` | `~/.local/share/wa/session.db` (XDG honored via `adrg/xdg`) |
| Config + allowlist | `$XDG_CONFIG_HOME/wa/{config.toml,allowlist.toml}` | same |
| Logs + audit | `$XDG_STATE_HOME/wa/{wa.log,audit.log}` | same |
| Socket | `$XDG_RUNTIME_DIR/wa/wa.sock` | `~/Library/Caches/wa/wa.sock` |
| Cache (media thumbnails) | `$XDG_CACHE_HOME/wa/` | same |

Permissions: `0700` on the data dir, `0600` on `session.db`, `0600` on the socket. SQLite store is **plaintext** — FileVault is documented as the boundary. SQLCipher is rejected because it requires CGO.

## Output schema

- Default: human-readable tables.
- `--json` switches to **NDJSON** with a versioned schema string in every object: `{"schema":"wa.event/v1", …}`. Claude Code plugins parse this; stability matters.
- Exit codes follow `sysexits.h`: `0` ok, `64` usage, `10` not-paired, `11` not-allowlisted, `12` rate-limited, `78` config error.

## Claude Code plugin integration

The plugin is a separate repo (`wa-assistant/`), not vendored here. This repo only ships the binaries it needs.

- **Slash commands** under `commands/` are markdown files with frontmatter; they shell out to `${CLAUDE_PLUGIN_DATA_DIR}/bin/wa <subcommand> --json` and ask Claude to summarize the result. `disable-model-invocation: true` on `send` so Claude cannot auto-trigger it.
- **`PreToolUse` hook on `Bash`** parses any `wa send` invocation, extracts `--to`, and validates against the allowlist file. Block on miss. This is the single most important defense against prompt injection.
- **Inbound** flows through Claude Code's [Channels API](https://code.claude.com/docs/en/channels). The daemon writes JSONL events to `${CLAUDE_RUNTIME_DIR}/channels/wa-assistant.sock`; a `Notification` hook formats the event, wraps it in `<untrusted-sender>` tags, and injects it. **Verify the exact channel transport against the live docs** before implementing — Anthropic revised this twice in 2025.
- **Bootstrap** of the `wa`/`wad` binaries does NOT happen via a plugin install lifecycle hook — Claude Code plugins have no `scripts.postInstall` field (verified against the official Telegram plugin source 2026-04-06). Install paths are: (a) `brew install yolo-labz/tap/wa`; (b) `nix profile install github:yolo-labz/wa`; (c) `go install github.com/yolo-labz/wa/cmd/wa@latest && go install .../cmd/wad@latest`; (d) a one-shot Bash skill `/wa:install` that `curl`s the GoReleaser release tarball matching the user's OS/arch. The launchd plist / systemd unit is written by `wad install-service` (a `wad` subcommand), not by the plugin. Never bundle binaries inside the plugin git repo.
- The plugin **must not** request `Bash(*)` or `Bash(wa:*)`. Enumerate exact subcommands: `Bash(${CLAUDE_PLUGIN_DATA_DIR}/bin/wa send:*)`, etc.

## Anti-patterns to avoid

1. **Leaking `whatsmeow/types.JID` into `internal/app` or `internal/domain`.** Enforced by `depguard`.
2. **Anemic domain.** If `domain/message.go` has no methods, it is a DTO package, not a domain. Put `Validate()`, `Truncate()`, and recipient checks on the types.
3. **One port per adapter method.** `MessageSender`, not `WhatsmeowSender`. One port per *capability the core needs*.
4. **Use-case-per-cobra-command.** Use cases must be reusable across primary adapters or hexagonal is theater.
5. **Mock-everything tests.** Prefer in-memory fakes in `internal/adapters/secondary/memory/`. They double as test fakes and as the seed for a future `--dry-run` mode.
6. **Java-flavored layering.** No factories, DTOs, mappers, or `usecase/interactor/presenter` trinity. Stay Go-flavored — structs, methods, small interfaces defined where they are consumed.
7. **HTTP-on-loopback for IPC.** The unix socket is private by file permissions and `LOCAL_PEERCRED`. HTTP needs tokens, gives nothing back.
8. **Encrypted-at-rest session DB via SQLCipher.** Adds CGO, breaks `go install`, FileVault is the documented boundary instead.
9. **In-process self-update.** `wa upgrade` should print the right `brew`/`nix profile upgrade` command, not replace its own binary.
10. **Bundling the Go binary inside the plugin git repo.** Multi-MB clones, no signing story. Download from GH Releases at install time.

## Reference projects to study

- [`tulir/whatsmeow`](https://github.com/tulir/whatsmeow) — the WA library and the `mdtest` example program.
- [`mautrix/whatsapp`](https://github.com/mautrix/whatsapp) — the most battle-tested whatsmeow consumer; read it for daemon lifecycle, pairing flow, and quirks the secondary adapter must absorb.
- [`AsamK/signal-cli`](https://github.com/AsamK/signal-cli) — closest functional analog. Steal from its `daemon` mode and JSON-RPC interface.
- [`tailscale/tailscale`](https://github.com/tailscale/tailscale) `client/tailscale/localclient.go` — the daemon-CLI split pattern.
- [`cli/cli`](https://github.com/cli/cli) — gold standard for Go CLI structure, cobra factory pattern, GoReleaser config.
- [`superfly/flyctl`](https://github.com/superfly/flyctl) — install script + `doctor` command pattern.
- [`ThreeDotsLabs/wild-workouts-go-ddd-example`](https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example) — the canonical 2024-refreshed Go hexagonal layout.
- [`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice) — the closest prior art; read but do not depend on (it is a REST server, not a CLI).

## First-week implementation order

Blocking — must be settled before line one of code:

1. **§Safety** — allowlist + rate limiter design. Build the brakes first.
2. **§Domain types** — `JID`, `Message`, `Allowlist`, `Action`. ~150 lines, zero dependencies.
3. **§Ports** — the 7 interfaces in `internal/app/ports.go`.
4. **§IPC wire protocol** — JSON-RPC method list, error code map.
5. **§FS layout** — paths nailed down; no later moves.
6. **Composition root** — `cmd/wad/main.go` wires `whatsmeow` adapter → use cases → socket server. Smallest possible end-to-end: pair + send.

Deferrable to v0.1:

- Pairing UX polish (start with QR-in-terminal, add `wa login --phone` later).
- FTS5 message cache.
- `wa doctor`.
- GoReleaser pipeline + notarization.
- Nix flake.

Deferrable past v0.1:

- Multi-profile support (but namespace `config.toml` so `[profile.work]` can be added without breakage).
- REST/MCP primary adapters.
- Channels inbound integration (do this once `wad` reliably stays paired for a week).
- Self-update.
- Encrypted-at-rest session DB.

## OPEN questions — all resolved on 2026-04-06

All questions originally in this section were answered by the research swarm in `specs/001-research-bootstrap/research.md`. Summary:

- **Pairing default** → QR-in-terminal, with `--pair-phone <E164>` opt-in flag (matches `mdtest`, `mautrix-whatsapp`, `signal-cli`, WhatsApp's own client). See research §OPEN-Q1.
- **Repo visibility** → public, `github.com/yolo-labz/wa`, default branch `main`. Already created. See research §OPEN-Q2.
- **Module path** → `github.com/yolo-labz/wa`. Already in `go.mod`. See research §OPEN-Q2.
- **Channels API** → confirmed real (research preview, v2.1.80+, claude.ai login required). The plugin layer uses Channels = MCP server per Anthropic's design; the CLI/daemon stays MCP-free. See research §OPEN-Q3.
- **Burner number** → none in this session; integration tests gated `WA_INTEGRATION=1`, manual only, never in CI. See research §OPEN-Q4.

Future open questions belong in the spec for whichever feature surfaces them, not here.

## Build/test commands

None yet — repo is empty. Once `go.mod` exists, the standard surface will be:

```
go build ./cmd/wa ./cmd/wad
go test ./...
go test -race -tags integration ./...   # requires WA_INTEGRATION=1 and a paired burner
golangci-lint run
goreleaser release --snapshot --clean
```

This section should be updated as soon as the first commit lands.
