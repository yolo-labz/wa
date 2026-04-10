# Research: Binaries and Composition Root

**Feature**: 006-binaries-wiring
**Date**: 2026-04-09
**Status**: Complete

## D1 — Pairer seam

**Decision**: Add a one-method `Pairer` port interface in `internal/app/ports.go`:
```go
type Pairer interface { Pair(ctx context.Context, phone string) error }
```

**Rationale**: The whatsmeow adapter already exposes `Adapter.Pair(ctx, phone)` with the exact signature needed. A `Pairer` port is the lightest hexagonal-clean solution: the `memory/` fake can trivially implement it, `porttest/` can assert it, and the composition root wires `&adapter`. Option (b) — passing a raw `func` — makes dependencies opaque and untestable via contract tests. Option (c) — exposing pairing outside the dispatcher — splits method routing. CLAUDE.md rule 20 (Cockburn: no fixed port count) permits a 9th port.

**Source**: `internal/adapters/secondary/whatsmeow/pair.go:47`; spec FR-029.

---

## D2 — TOML library

**Decision**: `github.com/BurntSushi/toml`.

**Rationale**: The allowlist file is tiny and fully owned by the daemon (regenerated on every mutation via atomic rename). Neither library preserves comments on round-trip marshal/unmarshal; since the daemon generates the file, that's fine. `BurntSushi/toml` has zero transitive dependencies, is the de facto Go standard, and is simpler than `pelletier/go-toml/v2` for this use case.

**Alternatives**: `pelletier/go-toml/v2` — more features (comment preservation via Tree API) but adds dependency weight for no benefit. Rejected.

**Source**: [BurntSushi/toml](https://github.com/BurntSushi/toml).

---

## D3 — File watcher for allowlist reload

**Decision**: `fsnotify/fsnotify` watching the **parent directory** (not the file directly) with a 100ms debounce timer. `SIGHUP` as fallback.

**Rationale**: Atomic write-then-rename (FR-017) deletes the watched inode on macOS/kqueue — watching the file directly loses the watcher after the first rename. The fix (documented in fsnotify issue #17): watch the parent directory and filter `Event.Name`. Double events from Spotlight/indexing are absorbed by the debounce. Linux inotify handles rename correctly but the parent-directory pattern is portable, so use it everywhere. SIGHUP remains per FR-022 — both paths work.

**Source**: [fsnotify#17](https://github.com/fsnotify/fsnotify/issues/17); [fsnotify docs](https://pkg.go.dev/github.com/fsnotify/fsnotify).

---

## D4 — testscript for CLI golden tests

**Decision**: `rogpeppe/go-internal/testscript` (already in `go.mod` at v1.14.1).

**Rationale**: `.txtar` files with shell-like scripts (`exec`, `stdout`, `stderr`, `cmp`) in an isolated temp dir. For Cobra CLIs, register via `testscript.RunMain(m, map[string]func() int{"wa": waMain})` in `TestMain`, then `exec wa send ...` in `.txtar` asserts output. Actively maintained, used by `gopls`, `goreleaser`, and `cue`.

**Pattern**: `cmd/wa/testdata/*.txtar`, one per subcommand. Error tests use `! exec wa send ...` for non-zero exit.

**Source**: [testscript pkg.go.dev](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript); `go.mod:9`.

---

## D5 — Cobra + fang

**Decision**: Use `charmbracelet/fang` (v2, import path `charm.land/fang/v2`).

**Rationale**: Fang is a thin Cobra enhancer wrapping `cobra.Command` execution via `fang.Execute(ctx, rootCmd)`. It provides: styled help/error output (Lip Gloss v2), automatic `--version` from build info, hidden `man` subcommand, `completion` subcommand for bash/zsh/fish. Integration is one function call replacing `cmd.ExecuteContext(ctx)`. Not deprecated — actively released in 2025.

**Source**: [charmbracelet/fang](https://github.com/charmbracelet/fang).

---

## D6 — slogaudit adapter

**Decision**: ~30 LoC adapter wrapping `slog.NewJSONHandler` over `os.OpenFile(path, O_APPEND|O_CREATE|O_WRONLY, 0600)`.

**Rationale**: The `AuditLog` port requires `Record(ctx, AuditEvent) error` with persist-before-return, concurrency-safe, reject out-of-order. `slog.JSONHandler` gives structured JSON for free. The adapter holds a `*slog.Logger` and a `sync.Mutex` for the out-of-order check (last-seen timestamp). `Record` calls `logger.LogAttrs`. File opened once in constructor; `Close()` closes the file. No buffering — each `Record` is a synchronous write.

**Source**: `internal/app/ports.go:96-101`; `internal/adapters/secondary/slogaudit/.gitkeep`.

---

## Summary of new dependencies

```
require (
    github.com/BurntSushi/toml       v1.x.x
    github.com/fsnotify/fsnotify     v1.x.x
    charm.land/fang/v2               v2.x.x   // or github.com/charmbracelet/fang
)
```

All other imports (`spf13/cobra`, `rogpeppe/go-internal/testscript`, `slog`, `x/sys/unix`) are already present.

## Summary of changes to existing code

| File | Change | Why |
|---|---|---|
| `internal/app/ports.go` | Add `Pairer` interface (1 method) | D1: hexagonal-clean pairing seam |
| `internal/adapters/secondary/memory/adapter.go` | Add `Pair` no-op | D1: tests need fake |
| `internal/adapters/secondary/whatsmeow/adapter.go` | (already satisfies `Pairer` via existing `Pair` method) | No change needed |
| `internal/adapters/secondary/slogaudit/` | NEW: `audit.go` (~30 LoC) | D6: durable audit log |
| `internal/app/dispatcher.go` | Add `Pairer` to `DispatcherConfig` | D1: wire through to pair handler |
| `internal/app/method_pair.go` | Call `d.pairer.Pair(ctx, phone)` instead of stub | D1: real pairing via port |
