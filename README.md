<div align="center">

# wa

**Personal WhatsApp automation CLI + daemon, written in Go.**

A hexagonal Go daemon that owns a WhatsApp Multi-Device session and a thin JSON-RPC client that talks to it — safe enough to let a language model send messages on your behalf, crash-safe enough to survive a power loss mid-migration, and paranoid enough to refuse every destructive flag you might expect.

[![CI](https://github.com/yolo-labz/wa/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yolo-labz/wa/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yolo-labz/wa?sort=semver)](https://github.com/yolo-labz/wa/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/yolo-labz/wa)](./go.mod)
[![License](https://img.shields.io/github/license/yolo-labz/wa)](./LICENSE)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-yellow.svg)](https://conventionalcommits.org)
[![Nix flake](https://img.shields.io/badge/nix-flake-5277c3?logo=nixos&logoColor=white)](./flake.nix)

[Quickstart](#quickstart) · [Install](#install) · [Manual](./docs/manual.md) · [Architecture](#architecture) · [Security](./SECURITY.md) · [Contributing](./CONTRIBUTING.md)

</div>

---

## What this is

Two binaries, one repo:

- **`wad`** — long-running daemon that owns the WhatsApp session, the SQLite ratchet store, and the websocket to `web.whatsapp.com`. Runs under `systemd` (Linux), `launchd` (macOS), or a NixOS module. Single-instance per profile, **never as root**.
- **`wa`** — thin JSON-RPC client that speaks to `wad` over a unix socket. This is what shell scripts, cron jobs, and Claude Code plugins actually invoke.

It is built on [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — the library that powers `mautrix-whatsapp` at production scale — because it is the only reverse-engineered WhatsApp library actively maintained in 2026. There is no MCP server in this repo by design.

## What this is NOT

- **Not** a bulk-messaging tool. The rate limiter is non-overridable and there is no `--force` flag anywhere.
- **Not** a multi-tenant SaaS. Each `wa` install is scoped to one person, with optional multi-profile isolation for work/personal splits.
- **Not** a Matrix bridge. Use [`mautrix-whatsapp`](https://github.com/mautrix/whatsapp) if that's what you want.
- **Not** a REST gateway. Use [`EvolutionAPI`](https://github.com/EvolutionAPI/evolution-api) or [`WAHA`](https://github.com/devlikeapro/waha) if that's what you want.
- **Not** the official WhatsApp Cloud API. This project uses the reverse-engineered Multi-Device protocol via `whatsmeow`.

## Quickstart

```bash
# Install (Nix — recommended for NixOS/nix-darwin users)
nix profile install github:yolo-labz/wa

# Or grab a release tarball
curl -LO https://github.com/yolo-labz/wa/releases/latest/download/wa_0.2.0_linux_amd64.tar.gz
tar xzf wa_0.2.0_linux_amd64.tar.gz -C ~/.local/bin

# Start the daemon (default profile)
wad &

# Pair your phone — QR code in terminal
wa pair

# Allowlist yourself (default-deny policy)
wa allow add 5511999999999@s.whatsapp.net --actions send

# Send a message
wa send --to 5511999999999@s.whatsapp.net --body "hello from wa"

# Install as a persistent system service
wad install-service --profile default
```

For the full tour including multi-profile setup, shell completion, migration, and the audit log, see **[`docs/manual.md`](./docs/manual.md)**.

## Install

### Homebrew (macOS + Linux)

```bash
brew install yolo-labz/tap/wa
```

### Nix flake

```bash
# One-shot
nix run github:yolo-labz/wa -- profile list

# Install to profile
nix profile install github:yolo-labz/wa

# Dev shell (go + gopls + golangci-lint + goreleaser + sqlite + jq)
nix develop github:yolo-labz/wa
```

**NixOS module** — import the system module and enable:

```nix
{
  inputs.wa.url = "github:yolo-labz/wa";
  outputs = { self, nixpkgs, wa, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      modules = [
        wa.nixosModules.default
        { services.wa.enable = true; services.wa.profile = "default"; }
      ];
    };
  };
}
```

**home-manager module** — import for a per-user installation:

```nix
{
  imports = [ wa.homeManagerModules.default ];
  services.wa = {
    enable = true;
    profile = "default";
    autoStart = true;
  };
}
```

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
go install github.com/yolo-labz/wa/cmd/wa@latest
go install github.com/yolo-labz/wa/cmd/wad@latest
```

## Multi-profile

Run two WhatsApp accounts side-by-side — personal and work, or one per client — with full process isolation. Each profile has its own `session.db`, `allowlist.toml`, `audit.log`, rate limiter, unix socket, and warmup timestamp.

```bash
wa profile create work
wad --profile work &
wa --profile work pair
wa --profile work status

wa profile list
# PROFILE   ACTIVE  STATUS      JID                          LAST_SEEN
# default   *       connected   5511999999999@s.whatsapp.net 2026-04-11T17:00:00Z
# work              connected   5511888888888@s.whatsapp.net 2026-04-11T17:00:00Z
```

Profile selection precedence (highest wins):

1. `--profile <name>` flag
2. `WA_PROFILE` env var (empty = unset)
3. `$XDG_CONFIG_HOME/wa/active-profile` pointer
4. Singleton (if exactly one profile exists)
5. Literal `default`

See [`docs/manual.md` §4](./docs/manual.md#4-multi-profile-cookbook) and [`specs/008-multi-profile/`](./specs/008-multi-profile) for the full spec + crash-safe migration design.

## Safety

`wa` is safe enough to let a language model invoke it on your behalf. The safety pipeline is **non-overridable** and lives inside the daemon, below every RPC path:

- **Allowlist**, default-deny. Per-action (`read` / `send` / `group.add` / `group.create`). Hot-reloaded on SIGHUP. Mutated via `wa allow add/remove`.
- **Rate limiter**. 2/sec, 30/min, 1000/day per profile. No `--force` flag. Ever.
- **Warmup ramp**. Fresh sessions run at 25 % caps for days 0–7, 50 % for days 8–14, 100 % after. Timestamp sourced from the session store so daemon restarts don't reset the clock.
- **Audit log**. Append-only JSON Lines at `$XDG_STATE_HOME/wa/<profile>/audit.log`. Every send + every allowlist decision + every migration is recorded. Never auto-rotated — back it up yourself.
- **Inbound prompt-injection firewall**. Inbound message bodies are wrapped in `<channel source="wa" ...>…</channel>` tags before reaching Claude Code so the model can structurally distinguish "the user typed this in the terminal" from "an unknown contact sent this".
- **Crash-safe migration**. 007→008 migration uses a write-ahead marker + single `os.Rename` pivot + fsync barriers. Proven by a subprocess `SIGKILL` injection test that kills the process between every step and asserts zero data loss on recovery.
- **Socket hardening**. Unix socket + sibling lockfile opened with `O_NOFOLLOW` (CVE-2025-68146), parent directory verified mode `0700` + euid-owned, `SO_PEERCRED` check on every accept, umask-narrowed bind.

Full threat model: [`SECURITY.md`](./SECURITY.md).

## Architecture

```
                  ┌──────────────────────────┐
                  │         cmd/wa           │  thin JSON-RPC client
                  │  (cobra, no business     │  (stateless, re-invokable,
                  │   logic, no state)       │   what scripts actually call)
                  └─────────────┬────────────┘
                                │  JSON-RPC 2.0 over
                                │  $XDG_RUNTIME_DIR/wa/<profile>.sock
                                ▼
┌──────────────────────────────────────────────────────────────┐
│                         cmd/wad                              │  composition root
│                                                              │  (one process per profile)
│   ┌─────────────────────────┐    ┌────────────────────────┐  │
│   │ internal/app/dispatcher │◄──►│ Safety pipeline:       │  │
│   │  (use cases, port-only) │    │  allowlist + rate lim  │  │
│   │                         │    │  + warmup + audit      │  │
│   └───────────┬─────────────┘    └────────────────────────┘  │
│               │                                              │
│               ▼  9 port interfaces                           │
│   ┌───────────────────────────────────────────────────────┐  │
│   │             internal/adapters/secondary              │  │
│   │  whatsmeow | sqlitestore | sqlitehistory | memory    │  │
│   │  slogaudit | …                                       │  │
│   └───────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
                                │
                                │  Signal Protocol ratchets +
                                │  WhatsApp Multi-Device websocket
                                ▼
                        web.whatsapp.com
```

- **Hexagonal core** (`internal/domain/`, `internal/app/`) depends only on the 9 port interfaces in `internal/app/ports.go`. Not one import of `go.mau.fi/whatsmeow` outside the adapters, enforced by a `golangci-lint depguard` rule that is CI-blocking.
- **Port-boundary fakes**: every secondary adapter ships an in-memory twin for tests. The contract test suite (`internal/app/porttest/`) runs against any adapter. No test reaches the real websocket.
- **One daemon per profile**. Feature 008 bounds the blast radius: a crash in one profile's daemon never touches another. Trade-off: ~30 MB RSS per profile, documented in `specs/008-multi-profile/spec.md`.
- **No CGO**. Ever. `modernc.org/sqlite` is the only SQLite path. Enforced in the Nix flake (`env.CGO_ENABLED = "0"`), in GoReleaser, and in the `go.mod` toolchain flags.

For the full design, the 23 reliability rules, anti-patterns to avoid, and the speckit workflow, read [`CLAUDE.md`](./CLAUDE.md).

## Command reference (at a glance)

Client (`wa`):

| Command | Purpose |
|---|---|
| `wa pair` | Scan a QR or use `--phone` for phone-code pairing |
| `wa status` | Non-blocking connection state |
| `wa send --to <jid> --body <text>` | Send a text message (allowlist + rate limiter apply) |
| `wa sendMedia --to <jid> --path <file>` | Send an image/video/audio/document |
| `wa markRead --chat <jid> --messageId <id>` | Mark a message as read |
| `wa react --chat <jid> --messageId <id> --emoji 👍` | Add/remove a reaction |
| `wa groups` | List joined groups |
| `wa allow add <jid> --actions send,read` | Grant actions |
| `wa allow remove <jid>` | Revoke all actions |
| `wa allow list` | Dump the allowlist |
| `wa wait --events message --timeout 30s` | Block until an event arrives |
| `wa profile list/use/create/rm/show` | Multi-profile lifecycle |
| `wa migrate [--dry-run\|--rollback]` | Explicit 007→008 migration |
| `wa panic` | Unlink device + wipe local session |
| `wa version` | Version, commit, build date |
| `wa upgrade` | Print the upgrade command for your install method |
| `wa completion bash\|zsh\|fish\|powershell` | Shell completion script |

Daemon (`wad`):

| Command | Purpose |
|---|---|
| `wad [--profile <name>] [--log-level <lvl>]` | Run the daemon in the foreground |
| `wad install-service --profile <name>` | Install systemd/launchd unit for a profile |
| `wad uninstall-service --profile <name>` | Remove only the specified profile's unit |
| `wad migrate [--dry-run\|--rollback]` | Internal target for `wa migrate` |

Full flag reference: [`docs/manual.md §6`](./docs/manual.md#6-subcommand-reference).

## Development

```bash
# Clone
git clone git@github.com:yolo-labz/wa.git
cd wa

# Devshell (Nix users)
nix develop

# Or use your own Go toolchain (1.22+)
go version

# Build
go build ./cmd/wa ./cmd/wad

# Test (race detector on by default; ~20s wall clock)
go test -race ./...

# Lint
golangci-lint run

# Format
gofumpt -w .

# Nix build + smoke test
nix build .#default && ./result/bin/wa version

# Snapshot release (local only, no publish)
goreleaser release --snapshot --clean --skip=publish
```

**Workflow**: every change lands via PR against `main`, commit subjects follow Conventional Commits, and feature work happens via [speckit](https://github.com/github/spec-kit) under `specs/NNN-<name>/`. See [`CONTRIBUTING.md`](./CONTRIBUTING.md).

**CI/CD** runs on a self-hosted GitHub Actions runner pool (label set `[self-hosted, dokku]`). Jobs: `detect`, `lint` (golangci-lint), `test` (race + coverage + SonarQube upload), `sonar` (scan), `govulncheck`, `nix` (`nix flake check` + `nix build .#default` + smoke test), `commitlint` (PR title). Release workflow triggers on `v*` tags and publishes GoReleaser tarballs + checksums + auto-generated CHANGELOG to GitHub Releases, with optional Apple notarization and Homebrew tap publication gated on secrets being configured (graceful-degrade otherwise).

## Project status

| Version | Features | Highlights |
|---|---|---|
| **v0.2.0** | 001–008 | Multi-profile support, crash-safe migration, hardened systemd template unit + launchd plist, all benchmarked SCs pass with 7.6×–870× headroom |
| v0.1.0 | 001–007 | First "shippable" tag (release workflow was broken, no artifacts published — fixed in v0.2.0) |
| v0.0.1–v0.0.6 | feature tags, no releases |

Deferrable past v0.1 (per [`CLAUDE.md`](./CLAUDE.md)): FTS5 message cache, `wa doctor`, REST/MCP primary adapters, Channels inbound integration, self-update, encrypted-at-rest session DB.

## Who this is for

One person — the maintainer. Multi-tenancy, hosted SaaS, and group-bulk-messaging use cases are explicitly out of scope. The entire safety story assumes a single-user threat model where FileVault / LUKS is the encryption boundary and `wa panic` is the recovery button. If that's not what you want, use one of the alternatives listed in [What this is NOT](#what-this-is-not).

## License

[Apache-2.0](./LICENSE). The `go.mau.fi/whatsmeow` upstream is MPL-2.0, which is file-level copyleft and does not propagate to consumers (Mozilla MPL FAQ Q9–Q11). The Apache choice matches the precedent set by Anthropic's official Telegram channel plugin and gives an explicit patent grant. See [`specs/001-research-bootstrap/research.md §OPEN-Q5`](./specs/001-research-bootstrap/research.md) for the rationale.

## Acknowledgements

- [`tulir/whatsmeow`](https://github.com/tulir/whatsmeow) — Tulir Asokan and the mautrix project, for the only WhatsApp library worth using in 2026.
- [`AsamK/signal-cli`](https://github.com/AsamK/signal-cli) — for proving that a daemon-plus-thin-CLI architecture for an end-to-end-encrypted messenger is achievable in a single binary.
- [`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice) — closest prior art, solving a different shape of the problem (REST gateway vs CLI daemon).
- [`spf13/cobra`](https://github.com/spf13/cobra) + [`creachadair/jrpc2`](https://github.com/creachadair/jrpc2) + [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) — the three load-bearing Go libraries that make this project CGO-free, testable, and pleasant to maintain.
- [`rogpeppe/go-internal`](https://github.com/rogpeppe/go-internal) — for `lockedfile` and `testscript`, both of which the project leans on heavily.
