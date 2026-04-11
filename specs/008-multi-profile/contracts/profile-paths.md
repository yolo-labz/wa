# Contract: Profile Path Layout

**Feature**: 008-multi-profile

This contract defines the canonical filesystem layout for a profile and the rules for deriving paths from a profile name. Every adapter and binary in the project is bound by this contract.

## Canonical layout

Given a validated profile name `<p>`:

### Per-profile state (subdirectories)

| Resource | Path (Linux) | Path (darwin) |
|---|---|---|
| Session DB | `$XDG_DATA_HOME/wa/<p>/session.db` | `~/Library/Application Support/wa/<p>/session.db` |
| History DB | `$XDG_DATA_HOME/wa/<p>/messages.db` | `~/Library/Application Support/wa/<p>/messages.db` |
| Allowlist TOML | `$XDG_CONFIG_HOME/wa/<p>/allowlist.toml` | `~/.config/wa/<p>/allowlist.toml` |
| Audit log | `$XDG_STATE_HOME/wa/<p>/audit.log` | `~/Library/Application Support/wa/<p>/audit.log` |
| Daemon log | `$XDG_STATE_HOME/wa/<p>/wad.log` | `~/Library/Application Support/wa/<p>/wad.log` |

### Per-profile runtime (flat files)

| Resource | Path (Linux) | Path (darwin) |
|---|---|---|
| Unix socket | `$XDG_RUNTIME_DIR/wa/<p>.sock` | `~/Library/Caches/wa/<p>.sock` |
| Lock file | `$XDG_RUNTIME_DIR/wa/<p>.lock` | `~/Library/Caches/wa/<p>.lock` |
| Pair HTML | `$TMPDIR/wa-pair-<p>.html` | `$TMPDIR/wa-pair-<p>.html` |

**Flat socket directory** is a deliberate choice — sockets live in a single directory per host so `ls $XDG_RUNTIME_DIR/wa/` enumerates all running daemons at a glance. This matches Emacs's `$XDG_RUNTIME_DIR/emacs/server` and systemd's `foo@instance.socket` conventions (research D5).

### Shared (profile-less)

| Resource | Path | Rationale |
|---|---|---|
| Cache directory | `$XDG_CACHE_HOME/wa/` | whatsmeow media is SHA-256 content-addressed; safe to share |
| Thumbnails subdir | `$XDG_CACHE_HOME/wa/thumbnails/` | Same |
| Active profile pointer | `$XDG_CONFIG_HOME/wa/active-profile` | Single file, not per-profile |
| Schema version | `$XDG_CONFIG_HOME/wa/.schema-version` | Single file, global |

## Directory permissions

All per-profile directories are created with mode `0700`. All files within are written with mode `0600` or stricter. The socket file has mode `0600` enforced by feature 004's pre-flight checks (unchanged).

## Runtime directory verification (security-critical)

Before calling `net.Listen("unix", socketPath)`, the daemon MUST verify the socket parent directory state. `os.MkdirAll` alone is insufficient — it honors umask, does not verify ownership, and does not reject symlinks.

Required pre-bind checks on the socket parent directory (`$XDG_RUNTIME_DIR/wa/` on Linux, `~/Library/Caches/wa/` on darwin):

1. **Exists and is a directory** via `Lstat` (NOT `Stat` — must catch symlinks to directories).
2. **Mode is exactly `0700`** (not `0750`, not `0755`). Any group/other bit is a refusal.
3. **Owned by `os.Geteuid()`**. An attacker with write access to the parent but a different UID is rejected.
4. **Not a symlink** (confirmed by `Lstat` + `Mode()&os.ModeSymlink == 0`).

All four checks are performed via `fstatat(AT_SYMLINK_NOFOLLOW)` on Linux or `lstat(2)` on darwin. Any violation is a hard fail at exit code 78 with a message naming the specific violated property.

If the parent directory does not exist, the daemon creates it with an explicit `Mkdir(path, 0700)` (not `MkdirAll` — `MkdirAll` creates intermediate directories with `0777 & ~umask`, which is wrong for this use case) and re-runs the four checks to confirm the result.

## TOCTOU mitigation at socket bind

`net.Listen("unix", path)` calls `bind(2)` which creates the socket file with permissions derived from the current umask. There is a small window between `bind` and any subsequent `os.Chmod` during which the socket is visible with umask-derived perms. An attacker running as the same UID can `connect()` during this window.

Mitigation: bind inside the already-verified `0700` parent directory. Because the parent is inaccessible to any other UID (and same-UID processes are part of the threat-model-out-of-scope set per the spec's Threat Model section), the TOCTOU window is closed by enclosure rather than by timing.

Additional belt-and-braces: `syscall.Umask(0177)` immediately before the listen call, restore afterwards. This ensures the socket is created with `0600` even if the chmod race is lost.

## Lockfile open discipline (CVE-2025-68146)

The `.lock` sibling file (`<socket>.lock`) MUST be opened with `O_NOFOLLOW` to prevent symlink-planting attacks of the class described in CVE-2025-68146 (filelock/Python, Nov 2025). The canonical Go `rogpeppe/go-internal/lockedfile` package does not set `O_NOFOLLOW` as of the pinned version — the project MUST either (a) wrap `lockedfile.OpenFile` to pass `O_NOFOLLOW` on the underlying `os.OpenFile` call, or (b) vendor a patched fork.

The lockfile open sequence:

```
fd = openat(parent_fd, name, O_CREATE|O_RDWR|O_NOFOLLOW|O_CLOEXEC, 0600)
flock(fd, LOCK_EX|LOCK_NB)
```

On `O_NOFOLLOW` rejection (`ELOOP`), the daemon MUST refuse to start and emit a clear error naming the offending path. Do not silently delete the symlink and retry — that permits a race.

## Peer credential check on accept (FR-045)

Every accepted connection MUST have its peer credentials verified before any JSON-RPC is dispatched:

- **Linux**: `getsockopt(fd, SOL_SOCKET, SO_PEERCRED, &ucred)` — credentials captured at `connect()` time, immutable thereafter.
- **darwin**: `getpeereid(fd, &euid, &egid)` OR `getsockopt(fd, SOL_LOCAL, LOCAL_PEEREPID, &epid)` — prefer `LOCAL_PEEREPID` for effective-uid semantics.

If `ucred.Uid != os.Geteuid()` (Linux) or `euid != os.Geteuid()` (darwin), the connection is rejected with JSON-RPC error `-32000` and a dedicated `audit:reject-uid` audit entry is written.

This check is belt-and-braces over `0600` file permissions. It protects against the "someone accidentally `chmod 0644`'d the socket" class of mis-configuration.

## Accept audit logging (FR-046)

Every accepted connection (not just mutating calls) MUST write one audit entry with action `audit:accept`, actor `wad:<profile>`, and detail containing the peer UID and PID. This provides forensic traceability if the socket is ever mis-permissioned or if a same-UID compromise is suspected.

Rate limiting: the accept audit entries MAY be coalesced — if the same peer PID connects more than 10 times in 1 second, the daemon MAY log one coalesced entry instead of 10 individual ones. Mutating JSON-RPC calls continue to produce one audit entry per call regardless.

## App Sandbox non-goal (darwin)

macOS clients running inside an App Sandbox container CANNOT connect to the `~/Library/Caches/wa/<profile>.sock` unix socket regardless of filesystem permissions. This is an Apple platform limitation — sandboxed processes are blocked from connecting to non-sandboxed local sockets by the kernel enforcement in `sandbox(7)`. Documented at [Apple DTS forum 126059](https://developer.apple.com/forums/thread/126059) and [788364](https://developer.apple.com/forums/thread/788364).

**Non-goal**: `wa` does not support invocation from inside an App Sandbox container. Users who want to script `wa` from sandboxed tooling (e.g., an Automator workflow, an Xcode build phase under sandbox) must use an unsandboxed terminal or provide a helper XPC service (out of scope for this feature).

## Profile name validation

Every path-producing function takes a profile name and MUST call `ValidateProfileName(name)` before using it. Validation rules:

```
regex:   ^[a-z][a-z0-9-]{0,30}[a-z0-9]$
length:  2-32 characters
reserved: [".", "..", "con", "prn", "aux", "nul", "com1"-"com9",
           "lpt1"-"lpt9", "root", "system", "wa", "wad"]
```

The name `default` is **explicitly allowed** — it IS the canonical default profile.

A violation returns `ErrInvalidProfileName` (exit code 64) or `ErrReservedProfileName` (exit code 64). Both error messages include the offending name and the rule that was violated.

## Runtime sun_path budget check (both platforms)

`wad` MUST compute the byte length of the final socket path at startup **including the NUL terminator implied by `sun_path`** and refuse to start if it exceeds the platform limit:

- **darwin**: `len(socketPath) + 1 <= 104` (i.e., printable path `<= 103` bytes). The 104-byte limit is from xnu `bsd/sys/un.h`, unchanged since 4.4BSD.
- **Linux**: `len(socketPath) + 1 <= 108` (i.e., printable path `<= 107` bytes). Per `unix(7)`.

Failure is a hard error at exit code 78 with a message naming the computed length, the limit, and the offending profile name. Do NOT warn-and-continue — a socket bind with a truncated path is a silent foot-gun.

Budget computation for darwin worst case:
```
/Users/<username≤32>/Library/Caches/wa/<profile>.sock\0
= 33 + 32 + 5 + 1 + len(profile) + 1  (NUL)
= 72 + len(profile)
```

With profile max length 32, the worst-case path is 104 bytes — right at the limit. Users with home directories longer than 32 bytes will see the error and must use a shorter profile name.

## Backward compatibility

**Pre-008 single-profile layout**:

| Resource | 007 path | 008 path (after migration) |
|---|---|---|
| Session DB | `$XDG_DATA_HOME/wa/session.db` | `$XDG_DATA_HOME/wa/default/session.db` |
| History DB | `$XDG_DATA_HOME/wa/messages.db` | `$XDG_DATA_HOME/wa/default/messages.db` |
| Allowlist | `$XDG_CONFIG_HOME/wa/allowlist.toml` | `$XDG_CONFIG_HOME/wa/default/allowlist.toml` |
| Audit log | `$XDG_STATE_HOME/wa/audit.log` | `$XDG_STATE_HOME/wa/default/audit.log` |
| Daemon log | `$XDG_STATE_HOME/wa/wad.log` | `$XDG_STATE_HOME/wa/default/wad.log` |
| Socket | `$XDG_RUNTIME_DIR/wa/wa.sock` | `$XDG_RUNTIME_DIR/wa/default.sock` |

The migration transaction (see `contracts/migration.md`) performs these moves atomically on first 008 startup.

## Path resolution code contract

Every path-producing function in the codebase MUST be implemented via `PathResolver` methods (defined in `cmd/wad/profile.go` and mirrored in `cmd/wa/profile.go`). No ad-hoc `filepath.Join(xdg.DataHome, "wa", …)` calls are permitted outside the resolver.

Violation of this rule is caught by a new depguard or grep-based lint in the polish phase of feature 008.

## Test coverage requirement

Every path in the "Canonical layout" table above MUST be exercised by:
1. A unit test in `cmd/wad/profile_test.go` that asserts the exact string returned by the resolver method
2. A runtime check in `cmd/wad/main.go` that calls `ensureDirs(profile)` before any adapter construction
3. An integration test in `cmd/wad/integration_test.go` that runs the full pair→send cycle against each path
