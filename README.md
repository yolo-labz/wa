# wa — WhatsApp automation CLI

A personal-account WhatsApp automation CLI for macOS and Linux, intended to back a Claude Code plugin that turns WhatsApp into an AI-mediated personal assistant.

> **Status:** pre-alpha. The architectural blueprint is locked. The first source files have not been written. See [`CLAUDE.md`](./CLAUDE.md) for the full design and [`specs/001-research-bootstrap/`](./specs/001-research-bootstrap/) for the active feature spec and the research that produced it.

## What this is

Two binaries, one repo:

- **`wad`** — long-running daemon that owns the WhatsApp session, the SQLite ratchet store, and all I/O against `web.whatsapp.com`. Runs under `launchd` (darwin) or a `systemd` user unit (linux), single-instance, never as root.
- **`wa`** — thin CLI client that speaks line-delimited JSON-RPC 2.0 to `wad` over a unix socket at `$XDG_RUNTIME_DIR/wa/wa.sock`. This is what a Claude Code plugin's `Bash` tool actually invokes.

It is built on [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — the same library that powers the `mautrix-whatsapp` Matrix bridge — because it is the only reverse-engineered WhatsApp library actively maintained at production scale in 2026. There is no MCP server in this repo and there will not be one; the Claude Code plugin lives in a separate repository and shells out to `wa` exclusively.

## Why a daemon

A WhatsApp multi-device session holds Signal Protocol ratchets, an active websocket, and an app-state cursor. Re-pairing per `wa send` invocation is impossible: it costs 2–5 seconds of handshake, advances the ratchet, and looks like a reconnect storm to WhatsApp's anti-abuse systems. The daemon owns this state for the entire user session; the CLI is a dumb client that can be safely re-invoked thousands of times.

## Who this is for

One person — the maintainer. Multi-tenancy, hosted SaaS, and group-bulk-messaging use cases are explicitly out of scope. If you want a hosted REST gateway, use [Evolution API](https://github.com/EvolutionAPI/evolution-api) or [WAHA](https://github.com/devlikeapro/waha) instead. If you want a Matrix bridge, use [`mautrix-whatsapp`](https://github.com/mautrix/whatsapp).

## Status and roadmap

| Phase | What | Where |
|---|---|---|
| Blueprint | Architecture, decisions, anti-patterns, port interfaces | [`CLAUDE.md`](./CLAUDE.md) |
| **001 — Research and bootstrap** | **OPEN questions, scaffold, repo, this README** | [`specs/001-research-bootstrap/`](./specs/001-research-bootstrap/) |
| 002 — Domain and ports | Pure-Go domain types, the seven port interfaces, in-memory fakes | not started |
| 003 — whatsmeow secondary adapter | Wrap `*whatsmeow.Client` behind the ports | not started |
| 004 — Daemon and JSON-RPC socket | `cmd/wad` composition root, single-instance lock, IPC server | not started |
| 005 — `wa` CLI client | `cmd/wa` cobra+fang client, JSON output schema | not started |
| 006 — Distribution | GoReleaser, Homebrew tap, Nix flake, notarization | not started |
| 007 — Claude Code plugin | Separate `wa-assistant` repo | not started |

## Build prerequisites

- Go 1.22 or newer (the repo currently pins to whatever `go.mod` records)
- `git`
- A WhatsApp account on a phone you control
- macOS arm64 or Linux (amd64/arm64)

There are no `make`, `npm`, or `cargo` requirements. Once the first source file lands, the canonical commands will be:

```sh
go build ./cmd/wa ./cmd/wad
go test ./...
go test -race -tags integration ./...   # gated behind WA_INTEGRATION=1 + a paired number
golangci-lint run
```

The repository deliberately ships **no `wa` binary**, no vendored dependencies, and no generated artifacts. Everything is built from source.

## Safety

Outbound messages are gated by a non-overridable allowlist + rate limiter that lives inside `wad`. There is no `--force` flag, ever. Inbound message bodies are wrapped in `<untrusted-sender>` tags before they reach Claude Code so that prompt injection from a contact cannot promote text into a system prompt. See [`SECURITY.md`](./SECURITY.md) and [`CLAUDE.md`](./CLAUDE.md) §"Safety" for the full threat model and mitigations.

## License

This repository is licensed under the Mozilla Public License 2.0 — the same license as `go.mau.fi/whatsmeow` upstream. See [`LICENSE`](./LICENSE).

## Acknowledgements

- [`tulir/whatsmeow`](https://github.com/tulir/whatsmeow) — Tulir Asokan and the mautrix project, for the only WhatsApp library worth using in 2026.
- [`AsamK/signal-cli`](https://github.com/AsamK/signal-cli) — for proving that a daemon-plus-thin-CLI architecture for an end-to-end-encrypted messenger is achievable in a single binary.
- [`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice) — closest prior art, even though it solves a different shape of the problem (REST gateway vs CLI daemon).
