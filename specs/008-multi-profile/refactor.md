# Refactor Inventory: Multi-Profile Support

**Feature**: 008-multi-profile
**Date**: 2026-04-11
**Source**: 5 parallel exploration agents (path sweep, wiring sweep, adapter sweep, tests+packaging sweep, constitution+safety sweep)

This document maps every file, line, and architectural surface that must change (or explicitly does NOT need to change) to support running multiple WhatsApp accounts simultaneously via a per-daemon profile model.

---

## Executive Summary

**Refactor surface: medium.** Most of the codebase is already profile-ready thanks to dependency injection. The blast radius concentrates in three places:

1. **Path plumbing** in `cmd/wad/main.go` + `cmd/wa/root.go` + `cmd/wad/dirs.go` + `socket.Path()` — everything needs to take a profile name.
2. **whatsmeow adapter's `GetFirstDevice()` call** — the only hard architectural blocker; must become device-by-id or move to per-process sqlstore.
3. **Service installation templates** (launchd plist + systemd unit) — must accept a profile parameter and emit profile-aware labels.

Everything else (sqlitestore, sqlitehistory, slogaudit, socket.Server, dispatcher, safety pipeline, allowlist, rate limiter, audit) is **already parameterizable** because the composition root injects paths/state on construction.

**Estimated scope**: ~400 LoC of production changes + ~100 LoC of tests + service template updates. Smaller than any feature since 001.

**Two unrelated bugs surfaced during the sweep:**
- **Bug A**: `cmd/wad/main.go` hardcodes `SessionCreated: time.Now()` when constructing the dispatcher. The warmup multiplier resets on every restart instead of using the real pairing timestamp. This is broken for single-profile too — must be fixed as a prerequisite.
- **Bug B**: The `socket.Path()` signature is parameterless on both Linux and darwin, with no sibling `PathFor(profile)` function. Needs a new function, not a mutated one (backward compatibility).

---

## 1. Paths (swarm sweep 1.1)

Complete inventory of every path literal, every call site, and every file that would change.

### 1.1 Socket path

| File | Line | Current | Change needed |
|---|---|---|---|
| `internal/adapters/primary/socket/path_linux.go` | 19 | `Path() (string, error)` returns `$XDG_RUNTIME_DIR/wa/wa.sock` | Add `PathFor(profile string) (string, error)` returning `$XDG_RUNTIME_DIR/wa/<profile>.sock` when profile != "" |
| `internal/adapters/primary/socket/path_darwin.go` | 28 | `Path() (string, error)` returns `~/Library/Caches/wa/wa.sock` (hardcoded, NOT via xdg) | Same — add `PathFor(profile)` variant |

**Call sites** (all 3 must be updated):
- `cmd/wad/dirs.go:24` → `ensureDirs()` derives socket parent
- `cmd/wad/main.go:152` → `socket.Path()` passed to `server.Run`
- `cmd/wa/root.go:29` → default for `--socket` flag

**Lock file path**: `internal/adapters/primary/socket/lock.go:22` computes `<socketPath> + ".lock"` — already derived, no change.

### 1.2 Session DB path

| File | Line | Current | Change needed |
|---|---|---|---|
| `cmd/wad/main.go` | 55 | `filepath.Join(xdg.DataHome, "wa", "session.db")` | `filepath.Join(xdg.DataHome, "wa", profile, "session.db")` |
| `internal/adapters/secondary/sqlitestore/store.go` | 51 | Appends `.lock` via `lockedfile.Edit(dbPath + ".lock")` | No change — already takes a path |

Constructor `sqlitestore.Open(ctx, dbPath, log)` **already accepts a full path**. Zero changes to the adapter.

### 1.3 History DB path

| File | Line | Current | Change needed |
|---|---|---|---|
| `cmd/wad/main.go` | 63 | `filepath.Join(xdg.DataHome, "wa", "messages.db")` | `filepath.Join(xdg.DataHome, "wa", profile, "messages.db")` |

Constructor `sqlitehistory.Open(ctx, dbPath)` is already parameterized. Zero changes.

### 1.4 Audit log path

| File | Line | Current | Change needed |
|---|---|---|---|
| `cmd/wad/main.go` | 72 | `filepath.Join(xdg.StateHome, "wa", "audit.log")` | `filepath.Join(xdg.StateHome, "wa", profile, "audit.log")` |

`slogaudit.Open(path)` already accepts a path. Zero adapter changes.

### 1.5 Allowlist TOML path

| File | Line | Current | Change needed |
|---|---|---|---|
| `cmd/wad/main.go` | 82 | `filepath.Join(xdg.ConfigHome, "wa", "allowlist.toml")` | `filepath.Join(xdg.ConfigHome, "wa", profile, "allowlist.toml")` |
| `cmd/wad/main.go` | 98 | `watchAllowlist(ctx, allowlistPath, ...)` | No change (receives path as param) |
| `cmd/wad/allowlist.go` | 33, 74, 118 | `loadAllowlist(path)`, `saveAllowlist(path)`, `watchAllowlist(ctx, path, …)` | No change — already path-parameterized |
| `cmd/wad/methods.go` | 87, 133 | `saveAllowlist(allowlistPath, allowlist)` | No change — receives path from main.go |

### 1.6 Service file paths

| File | Line | Current | Change needed |
|---|---|---|---|
| `cmd/wad/service_darwin.go` | 16 | `plistLabel = "com.yolo-labz.wad"` (const) | Become function `plistLabel(profile)` → `com.yolo-labz.wad` (default) or `com.yolo-labz.wad.<profile>` |
| `cmd/wad/service_darwin.go` | 42 | `~/Library/LaunchAgents/com.yolo-labz.wad.plist` (hardcoded) | Derive from label: `<home>/Library/LaunchAgents/<label>.plist` |
| `cmd/wad/service_darwin.go` | 26 | Plist template `<string>{{.WadPath}}</string>` (single argument) | Add `<string>--profile</string><string>{{.Profile}}</string>` when profile != "" |
| `cmd/wad/service_darwin.go` | 52 | `logPath := filepath.Join(xdg.StateHome, "wa", "wad.log")` | `filepath.Join(xdg.StateHome, "wa", profile, "wad.log")` |
| `cmd/wad/service_linux.go` | 28-30 | `~/.config/systemd/user/wad.service` (hardcoded) | Either profile-named file (`wad.<profile>.service`) OR systemd template unit (`wad@.service` + instance name) |
| `cmd/wad/service_linux.go` | 19 | `ExecStart={{.WadPath}}` | `ExecStart={{.WadPath}} --profile {{.Profile}}` when profile != "" |

**Decision needed**: systemd template units (`wad@.service` with `%i` substitution) vs. one unit file per profile. Template units are more idiomatic but require `systemctl --user enable --now wad@work` syntax; per-profile files are simpler to manage from the install command. Research swarm D3 will settle this.

### 1.7 Pairing HTML path (hotfix branch)

The browser-pair hotfix in PR #8 writes to `os.TempDir() + "wa-pair.html"`. This is NOT profile-aware. If two daemons try to pair simultaneously they would overwrite each other's HTML. Fix: `os.TempDir() + "wa-pair.<profile>.html"`. **Depends on PR #8 landing first.**

### 1.8 Temp files, logs, other

- No other hardcoded paths in production code.
- Production code does NOT use `/tmp` directly (only tests and the pairing HTML).

---

## 2. Wiring (swarm sweep 1.2)

How the profile name flows through both binaries end-to-end.

### 2.1 `cmd/wad/main.go` — composition root

**Current startup sequence (line 41–150):**
1. L50 — `ensureDirs()`
2. L55 — session DB path
3. L63 — history DB path
4. L72 — audit log path
5. L82 — allowlist TOML path
6. L98 — start fsnotify watcher
7. L105 — `whatsmeow.Open(...)`
8. L117 — `app.NewDispatcher(...)`
9. L148 — `signal.NotifyContext(...)`

**Changes:**
- Add `--profile <name>` flag to `flag.String` parsing (before run()) with env var fallback `WA_PROFILE`. Default `""` (empty string = legacy behavior).
- L50 → `ensureDirs(profile)`
- L55, L63, L72, L82 → embed profile into all 4 paths
- L152 → `socket.PathFor(profile)` instead of `socket.Path()`

### 2.2 `cmd/wad/dirs.go` — directory creation

Currently creates 4 directories unconditionally: `xdg.DataHome/wa`, `xdg.ConfigHome/wa`, `xdg.StateHome/wa`, socket parent.

**Changes:**
- Signature becomes `ensureDirs(profile string) error`
- All 4 paths get `/<profile>` segment when profile != ""
- Backward compat: empty profile uses current behavior

### 2.3 `cmd/wad/allowlist.go`

**Zero changes.** All three functions (`loadAllowlist`, `saveAllowlist`, `watchAllowlist`) already take a `path string` parameter. Caller in `main.go` is the only place that needs the profile-aware path computation.

### 2.4 `cmd/wad/service.go` + `service_darwin.go` + `service_linux.go`

**Changes:**
- `installServiceCmd` gains `--profile <name>` flag (separate from the daemon's profile)
- `generateServiceFile(profile string)` threads profile into the template
- `installService()` and `uninstallService()` derive the service file path from the profile
- Darwin: label `com.yolo-labz.wad` (default) or `com.yolo-labz.wad.<profile>`
- Linux: unit file `wad.service` (default) or `wad@<profile>.service` or `wad.<profile>.service`
- Both templates emit `--profile <profile>` as a CLI argument to `wad` when profile is non-empty

### 2.5 `cmd/wa/root.go` — CLI root command

**Current global flags:** `--socket`, `--json`, `--verbose`

**Changes:**
- Add `flagProfile string` global
- Register `--profile` persistent flag with env var fallback `WA_PROFILE`, default `""`
- Socket default resolution (L28-30): use `socket.PathFor(flagProfile)` if profile set, else `socket.Path()`
- If user provides BOTH `--socket` and `--profile`, `--socket` takes precedence (explicit wins)

### 2.6 `cmd/wa/rpc.go` + subcommands

**Zero changes.** All subcommands call `callAndClose(flagSocket, method, params)` where `flagSocket` is already computed by root.go. The profile flows transparently via the socket path.

### 2.7 `internal/app/dispatcher.go` — use case layer

**Zero changes.** `DispatcherConfig` has no profile awareness by design — the dispatcher is profile-agnostic. Each profile gets its own dispatcher instance constructed in its own daemon process.

---

## 3. Adapters (swarm sweep 1.3)

Audit of every secondary adapter for single-profile assumptions.

| Adapter | Path param in constructor? | Package globals? | Can two instances coexist? | Refactor needed |
|---|---|---|---|---|
| `whatsmeow` | Receives containers, not paths | `store.DeviceProps` (mutated in Open()) | **NO — GetFirstDevice() blocker** | **MAJOR** |
| `sqlitestore` | Yes (dbPath) | None | YES | None |
| `sqlitehistory` | Yes (dbPath) | None | YES | None |
| `slogaudit` | Yes (path) | Sentinel errors only | YES | None |
| `socket` | Via `Run(ctx, socketPath)` | None | YES (different paths) | New `PathFor(profile)` sibling function only |
| `memory` | N/A (in-memory) | Sentinel errors only | YES | None |

### 3.1 Critical blocker: whatsmeow adapter

**File**: `internal/adapters/secondary/whatsmeow/adapter.go:150`
```go
device, err := container.GetFirstDevice(parentCtx)
```

This call retrieves the FIRST device in the whatsmeow sqlstore. If two daemons share the same sqlstore path, both load the same device. In the per-profile daemon model this is fine: each profile has its OWN `session.db` path, so each daemon's sqlstore has only its own device. **The blocker only manifests if we tried to share one sqlstore across profiles (Option B from the prior conversation), which we are not doing.**

**Verdict**: Not actually a blocker for per-profile-daemon model. Left in the refactor inventory as a warning for any future multi-tenant-daemon attempt.

**Additional concern**: `store.DeviceProps` is a package-level global mutated in `whatsmeow.Open()` (line ~163 in adapter.go). If two whatsmeow adapters opened in the same process (which we are NOT doing) would race on this global. Per-profile-daemon is safe because each process has its own globals.

### 3.2 sqlstore, sqlhistory, slogaudit

All three already accept full paths as constructor arguments. Zero code changes — the composition root just passes profile-aware paths.

### 3.3 socket.Server

The server constructor `NewServer(d, log, opts...)` does NOT take a path. The path is passed to `Run(ctx, socketPath)`. Two servers can run in the same process with different paths (though we'll have separate processes anyway). The file lock is instance-scoped per path via `lockedfile.Mutex`.

---

## 4. Tests + Packaging (swarm sweep 1.4)

### 4.1 Tests — blast radius is ZERO

All tests use `t.TempDir()` or memory adapters with no disk state. No test hardcodes a production path.

| Test file | Pattern | Multi-profile risk |
|---|---|---|
| `cmd/wad/main_test.go:27` | `t.TempDir()` for socket | None |
| `cmd/wad/integration_test.go` | Same pattern | None |
| `sockettest/*.go` (5 files) | `TempSocketPath(t)` helper | None |
| `sqlitestore/store_test.go:12` | `filepath.Join(t.TempDir(), "session.db")` | None |
| `sqlitehistory/store_test.go:11` | Same | None |
| `slogaudit/audit_test.go:20` | `filepath.Join(t.TempDir(), "state", "wa", "audit.log")` | None |
| `internal/app/dispatcher_test.go` | Hardcoded JID fixture + memory adapters | None (JIDs are test data, not paths) |
| `cmd/wa/cli_test.go`, `cmd_upgrade_test.go` | No path assumptions | None |

**New tests needed**: profile path resolution unit tests (`path_linux_test.go`, `path_darwin_test.go`), and an end-to-end two-profile integration test.

### 4.2 Packaging

| File | Assumption | Blast radius | Effort |
|---|---|---|---|
| `.goreleaser.yaml:96` | Single binary install `bin.install "wa"` + `bin.install "wad"` | Formula is generic; binaries are profile-neutral | **None** — profile is a runtime flag |
| `flake.nix:15` | `mainProgram = "wa"` | No assumption | **None** |
| `.github/workflows/release.yml` | No profile-specific logic | None | **None** |
| `cmd/wad/service_darwin.go` | `plistLabel = "com.yolo-labz.wad"` constant | Affects service install only | **Moderate** — make label profile-aware |
| `cmd/wad/service_linux.go` | Single unit at `~/.config/systemd/user/wad.service` | Same | **Moderate** — profile-aware unit name |
| `CLAUDE.md` | Documentation already mentions "multi-profile (deferred past v0.1)" in Decisions table | No code impact | **Update only** — move from deferred to implemented |

---

## 5. Constitution + Safety (swarm sweep 1.5)

### Principle-by-principle

| # | Principle | Impact | Action |
|---|---|---|---|
| I | Hexagonal core | No port interface changes; `DispatcherConfig` is profile-agnostic; depguard `app-no-adapters` rule unaffected | None |
| II | Daemon owns state | Each per-profile daemon owns its state exclusively | None |
| III | Safety first | Each daemon gets its own allowlist, rate limiter, warmup multiplier, audit log. Full isolation. | None |
| IV | CGO forbidden | No impact | None |
| V | Spec-driven with citations | This feature needs research.md with citations (swarm 2 in progress) | Pending |
| VI | Port-boundary fakes | Memory adapter already supports multiple instances | None |
| VII | Conventional commits | No impact | None |

### Safety pipeline per profile

Each per-profile daemon has:
- **Own `SafetyPipeline`** (internal/app/safety.go:9-19) instance with its own allowlist reference and rate limiter
- **Own `RateLimiter`** (internal/app/ratelimiter.go) with per-second/per-minute/per-day buckets — no shared state
- **Own warmup timestamp** sourced from the per-profile session's paired time

### Bug discovered: SessionCreated is `time.Now()` not the pairing timestamp

**File**: `cmd/wad/main.go:122` (approximately)
```go
SessionCreated: time.Now(),
```

**Impact**: The warmup multiplier should be computed from the actual pairing timestamp (persisted in the session store), not from daemon startup time. With the current code, restarting the daemon resets the warmup ramp — a day-3 session looks like day-0 after restart. **This is a pre-existing bug affecting single-profile as well.**

**Fix**: Source from `session.Load(ctx).CreatedAt()`. When the session is zero (not yet paired), default to `time.Now()` and update once pairing completes.

**Scope**: Fix as a prerequisite for feature 008, OR as a separate small PR before feature 008 starts implementation.

### Audit log dimensioning

**File**: `internal/domain/audit.go:86`
```go
type AuditEvent struct {
    Actor    string
    Action   AuditAction
    Subject  JID
    Decision string
    Detail   string
}
```

The `Actor` field is already available. Per-profile daemons can set `Actor: "wad:" + profile` (or just `profile`) to dimension audit entries. Each daemon's audit log is already physically separate, so this is more about conventions than infrastructure.

---

## 6. Decisions Requiring Research (swarm 2)

The following architectural choices cannot be made from code analysis alone and will be answered by the next swarm (web research with citations):

1. **systemd template unit (`wad@.service`) vs. one unit per profile (`wad.work.service`, `wad.personal.service`)** — which is more idiomatic and which integrates better with `systemctl --user`?
2. **macOS launchd multi-instance patterns** — is there a template-like mechanism, or must each profile have its own plist file?
3. **Profile naming conventions and validation rules** — what characters are allowed? How do other multi-profile Go CLIs validate profile names (gpg-agent, 1Password, kubectl context, aws-cli profile)?
4. **Precedent for profile-aware XDG paths in Go ecosystem** — is there a library? What do mature projects do?
5. **Default profile naming** — `default`, `main`, `personal`, empty string? What's the least surprising convention?

These will be answered by Swarm 2 and recorded in `research.md`.

---

## 7. Estimated Scope

| Category | Production LoC | Test LoC | Notes |
|---|---|---|---|
| `socket.PathFor(profile)` for linux + darwin | ~30 | ~40 | Two build-tagged files |
| `cmd/wad/dirs.go` profile segment | ~20 | ~20 | |
| `cmd/wad/main.go` flag parsing + path threading | ~50 | ~50 | Via env var fallback |
| `cmd/wad/service_darwin.go` + `service_linux.go` template + label derivation | ~80 | ~40 | |
| `cmd/wa/root.go` flag plumbing | ~20 | 0 | Unit tested indirectly |
| `cmd/wad/main.go` SessionCreated bug fix | ~10 | ~20 | **Prerequisite** |
| Pairing HTML profile suffix | ~10 | 0 | Depends on PR #8 |
| Two-profile integration test | 0 | ~100 | |
| CLAUDE.md updates | ~20 | 0 | Documentation |
| Quickstart + docs | ~30 | 0 | |
| **Total** | **~270** | **~270** | **~540 LoC** |

**Timeline estimate**: ~2-3 commits. Smallest feature since 001.

---

## 8. Risks and Open Questions

### Risks
1. **Pairing HTML collision** between profiles if they try to pair simultaneously. Mitigated by profile-suffixed temp filename.
2. **Resource overhead** — each additional daemon consumes ~30 MB RSS at minimum. Documented in assumptions; not a technical blocker.
3. **Service manager limits** — launchd has no documented cap on user agents, systemd has no cap on user units. Not a practical concern for N ≤ 20 profiles.
4. **Profile name injection into plist/unit** — must be validated to prevent shell/XML injection (e.g., a profile name containing `</string><key>RunAtLoad</key>` would break the plist). Validation rules come from swarm 2 research.

### Open questions (to be resolved by swarm 2)
1. Profile name allowed character set?
2. Default profile name convention?
3. systemd template vs. per-profile unit files?
4. Behavior when both `--socket` and `--profile` are provided on `wa` CLI?
5. How to handle the transition when the user's existing session exists at `session.db` (no profile subdir)? Auto-migrate to `default/session.db`?

---

## Next Steps

Swarm 2 (web research) is the next agent wave. Its output lands in `research.md` with citations for each decision. After that, spec writing begins with both documents as input.
