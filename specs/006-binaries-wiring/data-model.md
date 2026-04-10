# Data Model: Binaries and Composition Root

**Feature**: 006-binaries-wiring
**Date**: 2026-04-09

## `cmd/wad` — Composition root

### Construction sequence (FR-002)

```
1. Create XDG directories (dirs.go)
2. Open sqlitestore (session.db)
3. Open sqlitehistory (messages.db)
4. Open slogaudit (audit.log)
5. Load allowlist from allowlist.toml (or empty)
6. Start fsnotify watcher on allowlist parent dir
7. Open whatsmeow adapter (session + history + allowlist + logger)
8. Construct app.Dispatcher (all 9 ports + session timestamp)
9. Construct dispatcherAdapter (app.Event → socket.Event bridge)
10. Construct socket.Server (dispatcherAdapter)
11. socket.Server.Run(ctx) — blocks until ctx cancelled
12. Shutdown: reverse order (socket → dispatcher → whatsmeow → history → session → audit → watcher)
```

### dispatcherAdapter (FR-004)

```go
type dispatcherAdapter struct {
    d      *app.Dispatcher
    events chan socket.Event
    done   chan struct{}
}
```

- `Handle` delegates to `d.Handle`
- A goroutine reads `d.Events()`, converts `app.Event` → `socket.Event`, writes to `events`
- `Events()` returns `events`
- `Close()` waits for the goroutine to exit

~20 LoC per feature 005 research D2.

### Allowlist TOML schema

```toml
# $XDG_CONFIG_HOME/wa/allowlist.toml
[[rules]]
jid = "5511999999999@s.whatsapp.net"
actions = ["send", "read"]

[[rules]]
jid = "5511888888888@s.whatsapp.net"
actions = ["send"]
```

Go struct for marshal/unmarshal:
```go
type allowlistFile struct {
    Rules []allowlistRule `toml:"rules"`
}
type allowlistRule struct {
    JID     string   `toml:"jid"`
    Actions []string `toml:"actions"`
}
```

### Shutdown controller (FR-031..FR-035)

```
signal.NotifyContext(SIGINT, SIGTERM)
  → ctx cancelled
  → socket.Server.Shutdown() + Wait()
  → app.Dispatcher.Close()
  → whatsmeow.Adapter.Close()
  → sqlitehistory.Close()
  → sqlitestore.Close()
  → slogaudit.Close()
  → fsnotify.Watcher.Close()
```

Total deadline: 10 seconds hard cap. Each step logged at INFO.

## `cmd/wa` — CLI client

### Subcommand tree (FR-011)

```
wa
├── pair [--phone <E164>]
├── status [--json]
├── send --to <jid> --body <text> [--json]
├── sendMedia --to <jid> --path <file> [--caption <text>] [--mime <type>] [--json]
├── react --chat <jid> --messageId <id> --emoji <emoji> [--json]
├── markRead --chat <jid> --messageId <id> [--json]
├── groups [--json]
├── wait [--events <types>] [--timeout <duration>] [--json]
├── allow
│   ├── add <jid> --actions <actions>
│   ├── remove <jid>
│   └── list [--json]
├── panic
└── version

Global flags: --socket <path>, --json, --verbose, --help, --version
```

### JSON-RPC client (rpc.go)

Simple one-shot pattern per invocation:
```
1. Dial unix socket (socket.Path() or --socket override)
2. Send {"jsonrpc":"2.0","id":1,"method":"<verb>","params":{...}} + "\n"
3. Read one line response
4. Parse response: success → format output; error → map to exit code
5. Close connection
```

No persistent connection. No keepalive. Every `wa` invocation is a fresh dial.

### Exit code table (FR-015)

| JSON-RPC code | Exit code | Meaning |
|---|---|---|
| (success) | 0 | OK |
| `-32011` (NotPaired) | 10 | Service unavailable / not paired |
| `-32012` (NotAllowlisted) | 11 | Not allowlisted |
| `-32013` (RateLimited) | 12 | Rate limited |
| `-32014` (WarmupActive) | 12 | Warmup active (same bucket as rate) |
| `-32015` (InvalidJID) | 64 | Usage error |
| `-32016` (MessageTooLarge) | 64 | Usage error |
| `-32602` (InvalidParams) | 64 | Usage error |
| `-32601` (MethodNotFound) | 64 | Usage error |
| connection refused | 10 | Service unavailable |
| `-32003` (WaitTimeout) | 12 | Wait timeout |
| other | 1 | Generic error |

## slogaudit adapter (D6)

```go
type Audit struct {
    logger *slog.Logger
    file   *os.File
    mu     sync.Mutex
    lastTS time.Time
}
```

- `Open(path) (*Audit, error)` — creates parent dir 0700, opens file O_APPEND|O_CREATE|O_WRONLY 0600
- `Record(ctx, event) error` — acquires mu, checks `event.TS > lastTS` (out-of-order rejection), `logger.LogAttrs`, updates lastTS
- `Close() error` — closes file

~30 LoC production + ~30 LoC test.

## LOC budget

| Component | Estimate |
|---|---|
| `cmd/wad/main.go` | 120 |
| `cmd/wad/adapter.go` | 40 |
| `cmd/wad/allowlist.go` | 150 |
| `cmd/wad/dirs.go` | 40 |
| `cmd/wa/main.go` + `root.go` | 60 |
| `cmd/wa/cmd_*.go` (10 files) | 400 |
| `cmd/wa/rpc.go` | 80 |
| `cmd/wa/exitcodes.go` | 30 |
| `cmd/wa/output.go` | 50 |
| `slogaudit/audit.go` | 60 |
| Existing file changes (ports.go, memory, dispatcher, method_pair) | 50 |
| **Production subtotal** | **1080** |
| `cmd/wad/integration_test.go` | 100 |
| `cmd/wa/cli_test.go` + testdata/*.txtar | 200 |
| `slogaudit/audit_test.go` | 50 |
| **Test subtotal** | **350** |
| **Total** | **~1430** |
