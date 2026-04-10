# Implementation Plan: Binaries and Composition Root

**Branch**: `006-binaries-wiring` | **Date**: 2026-04-09 | **Spec**: [spec.md](spec.md)

## Summary

Deliver the two runnable binaries: `cmd/wad` (composition root wiring all adapters + use cases + socket server) and `cmd/wa` (thin Cobra CLI making JSON-RPC calls). Add the `slogaudit` adapter for durable audit logging, the `Pairer` port for hexagonal-clean pairing, `allowlist.toml` persistence with fsnotify watch, and the `panic` method for device unlinking. Uses `charmbracelet/fang` for styled Cobra output and `rogpeppe/go-internal/testscript` for CLI golden-file tests.

## Technical Context

**Language/Version**: Go 1.25 (toolchain pinned in `go.mod`)
**Primary Dependencies**:
- `spf13/cobra` — CLI subcommand tree (already decided in CLAUDE.md)
- `charm.land/fang/v2` — styled Cobra help/version/completion (D5)
- `github.com/BurntSushi/toml` — allowlist TOML persistence (D2)
- `github.com/fsnotify/fsnotify` — file watcher for allowlist hot-reload (D3)
- `rogpeppe/go-internal/testscript` — CLI golden tests (D4, already in go.mod)
- `slog` (stdlib) — structured logging + audit log adapter (D6)

**Storage**: SQLite via `sqlitestore` + `sqlitehistory` (existing), plus `allowlist.toml` (new, TOML file) and `audit.log` (new, append-only JSON lines).
**Testing**: `testscript` for CLI golden files, in-process integration test for wad, all existing unit tests.
**Constraints**: `CGO_ENABLED=0`, no whatsmeow imports in `internal/app/`, no use case logic in `cmd/`.

## Constitution Check

| # | Principle | Status | Notes |
|---|---|---|---|
| I | Hexagonal core | ✅ PASS | `Pairer` port added to `internal/app/ports.go` (D1). `cmd/wad` is the composition root — it's the ONLY place adapters and core meet. `cmd/wa` imports nothing from the hexagonal core. |
| II | Daemon owns state | ✅ PASS | `wad` owns all state. `wa` is a dumb JSON-RPC client with zero local state. |
| III | Safety first | ✅ PASS | Feature 005 already materialized this. Feature 006 wires it. The `allow` and `panic` methods complete the safety story. |
| IV | CGO forbidden | ✅ PASS | All new deps (toml, fsnotify, fang, cobra) are pure Go. |
| V | Spec-driven with citations | ✅ PASS | 6 D-blocks in research.md. |
| VI | Port-boundary fakes | ✅ PASS | `Pairer` fake in memory adapter. Integration test uses fake whatsmeow. |
| VII | Conventional commits | ✅ PASS | Inherited. |

## Project Structure

### Source Code

```text
cmd/
├── wad/
│   ├── main.go              # Composition root: construct → run → shutdown
│   ├── adapter.go           # dispatcherAdapter: app.Event → socket.Event (~20 LoC)
│   ├── allowlist.go         # Allowlist TOML load/save/watch + allow/panic JSON-RPC handlers
│   ├── dirs.go              # XDG directory creation on first start
│   └── integration_test.go  # //go:build integration — fake whatsmeow pair→allow→send
├── wa/
│   ├── main.go              # Cobra root + fang.Execute
│   ├── root.go              # Root command: --socket, --json, --verbose flags
│   ├── cmd_pair.go          # wa pair
│   ├── cmd_send.go          # wa send, wa sendMedia
│   ├── cmd_status.go        # wa status, wa groups
│   ├── cmd_wait.go          # wa wait
│   ├── cmd_allow.go         # wa allow [add|remove|list]
│   ├── cmd_panic.go         # wa panic
│   ├── cmd_react.go         # wa react
│   ├── cmd_markread.go      # wa markRead
│   ├── cmd_version.go       # wa version (via fang)
│   ├── rpc.go               # JSON-RPC client: dial socket, send/recv line-delimited JSON
│   ├── exitcodes.go         # JSON-RPC error code → sysexits exit code table
│   ├── output.go            # Human vs JSON output formatting
│   ├── cli_test.go          # testscript TestMain wiring
│   └── testdata/            # .txtar golden files per subcommand

internal/adapters/secondary/slogaudit/
├── audit.go                 # AuditLog implementation: slog.JSONHandler over append-only file
└── audit_test.go            # Test: write entries, read file, verify JSON lines

internal/app/
├── ports.go                 # EXISTING — add Pairer interface
├── dispatcher.go            # EXISTING — add Pairer to DispatcherConfig, wire pair handler
└── method_pair.go           # EXISTING — replace stub with d.pairer.Pair(ctx, phone)

internal/adapters/secondary/memory/
└── adapter.go               # EXISTING — add Pair no-op fake
```

## Complexity Tracking

| Change | Why | Simpler alternative rejected |
|---|---|---|
| 9th port (`Pairer`) | Pairing needs a hexagonal-clean seam. | Raw `func` — opaque, untestable via porttest. |
| fsnotify + debounce | Atomic rename breaks direct file watchers on macOS. | SIGHUP-only — worse UX, easy to forget. |
| fang v2 | Styled help, automatic --version, completion — all in one call. | Bare cobra — works but loses polish. |
