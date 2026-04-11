# Data Model: Multi-Profile Support

**Feature**: 008-multi-profile
**Date**: 2026-04-11

This feature adds no new domain types and no new port interfaces. It introduces five composition-root-level types that live in `cmd/wad/` and `cmd/wa/` and are invisible to the hexagonal core.

## 1. `Profile`

A named isolation boundary. This is a value type, not a struct — it's represented by a validated string throughout the codebase.

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `name` | `string` | Matches regex `^[a-z][a-z0-9-]{0,30}[a-z0-9]$`; not in reserved list | Lowercase, 2-32 chars, alpha start, alphanumeric end, hyphens allowed |

**Validation function**:
```go
// cmd/wad/profile.go
func ValidateProfileName(name string) error {
    if !profileNameRegex.MatchString(name) { return ErrInvalidProfileName }
    if isReserved(name) { return ErrReservedProfileName }
    return nil
}
```

**Reserved names** (rejected): `.`, `..`, `con`, `prn`, `aux`, `nul`, `com1`–`com9`, `lpt1`–`lpt9`, `root`, `system`, `wa`, `wad`. The name `default` is **explicitly allowed** — it IS the canonical default profile.

## 2. `PathResolver`

Derives all filesystem paths for a profile. Owned by the composition root; adapters receive pre-computed paths.

```go
// cmd/wad/profile.go
type PathResolver struct {
    profile string   // validated name
}

func NewPathResolver(profile string) (*PathResolver, error)

// Per-profile state paths
func (r *PathResolver) SessionDB() string   // $XDG_DATA_HOME/wa/<profile>/session.db
func (r *PathResolver) HistoryDB() string   // $XDG_DATA_HOME/wa/<profile>/messages.db
func (r *PathResolver) AllowlistTOML() string // $XDG_CONFIG_HOME/wa/<profile>/allowlist.toml
func (r *PathResolver) AuditLog() string    // $XDG_STATE_HOME/wa/<profile>/audit.log
func (r *PathResolver) WadLog() string      // $XDG_STATE_HOME/wa/<profile>/wad.log
func (r *PathResolver) SocketPath() string  // $XDG_RUNTIME_DIR/wa/<profile>.sock (flat)
func (r *PathResolver) LockPath() string    // SocketPath + ".lock"
func (r *PathResolver) PairHTMLPath() string // os.TempDir()/wa-pair-<profile>.html

// Shared (profile-less) paths
func (r *PathResolver) CacheDir() string    // $XDG_CACHE_HOME/wa/ — shared
func (r *PathResolver) ActiveProfileFile() string // $XDG_CONFIG_HOME/wa/active-profile
func (r *PathResolver) SchemaVersionFile() string // $XDG_CONFIG_HOME/wa/.schema-version

// Per-profile parent dirs (used by ensureDirs)
func (r *PathResolver) DataDir() string     // $XDG_DATA_HOME/wa/<profile>
func (r *PathResolver) ConfigDir() string   // $XDG_CONFIG_HOME/wa/<profile>
func (r *PathResolver) StateDir() string    // $XDG_STATE_HOME/wa/<profile>
func (r *PathResolver) SocketParentDir() string // $XDG_RUNTIME_DIR/wa (shared)
```

**Invariants**:
- All methods return absolute paths
- Paths are deterministic for a given profile + environment
- On darwin, `SocketPath()` uses `~/Library/Caches/wa/` (not `xdg.RuntimeDir` per feature 004's contradicts-blueprint note)
- The profile name has already been validated before `NewPathResolver` is called

## 3. `ProfileResolver` (active profile selection)

Resolves the effective profile name from the precedence chain.

```go
// cmd/wa/profile.go (and shared helper in cmd/wad)
type ProfileSource int
const (
    SourceFlag          ProfileSource = iota // --profile flag
    SourceEnv                                // WA_PROFILE env var
    SourceActiveFile                         // $XDG_CONFIG_HOME/wa/active-profile
    SourceDefault                            // literal "default"
    SourceSingleton                          // exactly one profile exists, use it
)

type ResolvedProfile struct {
    Name   string
    Source ProfileSource
}

// ResolveProfile returns the effective profile and its source.
// Order: flag > env > active-profile file > singleton (if exactly 1 profile) > "default".
// Returns an error at exit code 78 if multiple profiles exist and none is selected.
func ResolveProfile(flagValue string) (ResolvedProfile, error)
```

**Resolution algorithm**:
1. If `flagValue != ""`, return `{flagValue, SourceFlag}`
2. If `os.Getenv("WA_PROFILE") != ""`, return `{env, SourceEnv}`
3. Read `$XDG_CONFIG_HOME/wa/active-profile` — if exists and non-empty, return `{content, SourceActiveFile}`
4. List profiles via `filepath.Glob($XDG_DATA_HOME/wa/*/session.db)`:
   - If exactly one → return `{singletonName, SourceSingleton}`
   - If zero → return `{"default", SourceDefault}`
   - If more than one → return error exit code 78 with message listing available profiles

## 4. `MigrationTx` — migration transaction (REVISED 2026-04-11)

Encapsulates the crash-safe upgrade of a 007 single-profile layout to the 008 per-profile layout. Runs exactly once per 008 install, idempotent, recoverable from `SIGKILL` mid-flight. See `contracts/migration.md` for the full sequence.

```go
// cmd/wad/migrate.go
type MigrationTx struct {
    Logger   *slog.Logger
    DryRun   bool
    Rollback bool

    // Source paths (007 layout) — includes SQLite WAL/SHM sidecars
    srcSessionDB     string // $XDG_DATA_HOME/wa/session.db
    srcSessionWAL    string // $XDG_DATA_HOME/wa/session.db-wal (post-checkpoint, may be absent)
    srcSessionSHM    string // $XDG_DATA_HOME/wa/session.db-shm (post-checkpoint, may be absent)
    srcHistoryDB     string // $XDG_DATA_HOME/wa/messages.db
    srcHistoryWAL    string // $XDG_DATA_HOME/wa/messages.db-wal
    srcHistorySHM    string // $XDG_DATA_HOME/wa/messages.db-shm
    srcAllowlistTOML string // $XDG_CONFIG_HOME/wa/allowlist.toml
    srcAuditLog      string // $XDG_STATE_HOME/wa/audit.log
    srcWadLog        string // $XDG_STATE_HOME/wa/wad.log (if exists)

    // Target paths (008 layout, default profile)
    dstResolver *PathResolver // profile = "default"

    // Staging
    stagingDir   string // $XDG_DATA_HOME/wa/default.new (and equivalents)
    markerPath   string // $XDG_CONFIG_HOME/wa/.migrating
}

type MigrationStep struct {
    From string
    To   string
    Kind string // "copy" | "fsync" | "pivot" | "write-version" | "unlink" | "marker-write" | "marker-delete"
}

// Plan returns the sequence of operations without executing them.
// Includes pre-flight checks (EXDEV, free-space, ownership).
func (t *MigrationTx) Plan() ([]MigrationStep, error)

// Apply executes the transaction: flock → pre-flight → checkpoint WAL →
// write marker → stage copies → fsync → pivot → write schema version →
// unlink sources → delete marker → release flock.
// On failure before the pivot, removes staging dir and restores original.
// After the pivot, forward-only recovery completes the remaining steps.
func (t *MigrationTx) Apply() error

// Recover is called at startup when `.migrating` marker exists. It reads
// the marker, determines the furthest completed step, and either completes
// or rolls back the transaction to a clean state.
func (t *MigrationTx) Recover() error

// ApplyRollback reverses a completed migration IF schema version is still 2,
// no profiles other than default exist, no daemon is running, and no marker
// file exists.
func (t *MigrationTx) ApplyRollback() error
```

**Pre-conditions for Apply** (enforced in order):
1. No `.migrating` marker present (otherwise call `Recover()` first).
2. `srcSessionDB` exists as a regular file (007 layout detected).
3. `dstResolver.SessionDB()` does NOT exist AND `default.new/` does NOT exist (not yet migrated).
4. Schema version file does NOT exist OR equals `1`.
5. Source and destination parent directories reside on the **same filesystem** (`syscall.Stat_t.Dev` comparison; `EXDEV` refusal otherwise).
6. Destination has at least `2 × sum(source file sizes)` bytes free.
7. All source files are owned by `os.Geteuid()`.
8. No other process holds `session.db` open (checked via `lsof`/procfs; `flock` alone is insufficient because `modernc.org/sqlite` does not share it).

**WAL checkpoint step (new)**: before staging, `Apply()` opens `session.db` and `messages.db` with `modernc.org/sqlite`, issues `PRAGMA wal_checkpoint(TRUNCATE)` on each, and closes both handles. This collapses any pending WAL writes into the main file so the `-wal` and `-shm` sidecars become empty or disappear. Moving a WAL database **without** the checkpoint risks silent data loss of committed-but-not-checkpointed transactions.

**Post-conditions for Apply**:
- All 9 source files (3 session + 3 history + 3 config/state) moved to their `default/` subdirectory equivalents.
- Schema version file written with value `2` (atomic tempfile → fsync → rename → fsync parent).
- Active profile file written with value `default\n` (same atomic idiom).
- One audit log entry appended (action `migrate`, actor `wad:migrate`, decision `ok`).
- `.migrating` marker deleted.
- Source files unlinked.

**Crash safety** (replaces "atomicity" from earlier drafts): the transaction uses a **write-ahead marker + staging + single-pivot + fsync-everywhere** discipline. The `.migrating` marker is written and `fsync`ed **before any data moves**, converting startup recovery from an observation problem into a log-replay problem. The pivot is a single `renameat2(RENAME_EXCHANGE)` on Linux ≥3.15 or a two-step rename recorded in the marker on darwin/older Linux. Every copied file and every parent directory is `fsync`ed (`F_FULLFSYNC` on darwin per Apple's `fsync(2)` man page). See `contracts/migration.md` for the full 25-step sequence.

**Recovery** (new method): on startup, if `.migrating` exists, `Recover()` reads the marker to learn which step was last completed and either completes forward (if past pivot) or rolls back (if pre-pivot). Recovery is idempotent.

**Rollback pre-conditions** (for ApplyRollback):
- Schema version file exists and equals `2`
- `ls $XDG_DATA_HOME/wa/` lists exactly one directory: `default`
- All 9 destination files exist OR their absence matches pre-migration expected absence
- The active profile is `default`
- No `.migrating` marker present
- No daemon running (checked via `default.lock`)

## 5. `ProfileList` — dynamic profile enumeration

Derived at runtime from the filesystem, never from a sidecar registry.

```go
// cmd/wad/profile.go + cmd/wa/profile.go
type ProfileInfo struct {
    Name       string
    Active     bool
    Status     ProfileStatus // "connected" | "not-paired" | "daemon-stopped"
    JID        string        // empty if not paired
    DaemonPID  int           // 0 if not running
}

type ProfileStatus string
const (
    StatusConnected     ProfileStatus = "connected"
    StatusNotPaired     ProfileStatus = "not-paired"
    StatusDaemonStopped ProfileStatus = "daemon-stopped"
)

// ListProfiles enumerates profiles from the filesystem.
// Uses filepath.Glob($XDG_DATA_HOME/wa/*/session.db) as the source of truth.
// For each profile:
//   - Reads $XDG_CONFIG_HOME/wa/active-profile to flag the active one
//   - Attempts to connect to the profile's socket to determine StatusConnected/StatusDaemonStopped
//   - If the socket exists but no response, StatusDaemonStopped
//   - If the socket does not exist, StatusDaemonStopped (or StatusNotPaired if session.db is empty)
//   - Returns JID via a best-effort RPC call to the daemon's status method
func ListProfiles() ([]ProfileInfo, error)
```

**Enumeration method**: `filepath.Glob($XDG_DATA_HOME/wa/*/session.db)` — a profile exists iff its session directory contains a `session.db` file. Empty directories are NOT listed (they represent incomplete `wa profile create` runs).

## 6. Schema version tracking

A single file at `$XDG_CONFIG_HOME/wa/.schema-version` containing a single integer:
- `1` — pre-008 layout (implicit single profile, files at `wa/session.db` level)
- `2` — feature 008 layout (per-profile subdirs)

Absence of this file is treated as version 1 for backward compat. The file is written exactly once by the migration transaction.

Future features may bump this to `3`, `4`, etc. and add their own migration steps keyed on the version number. This is the hook that makes schema evolution forward-compatible.

## 7. Active profile file

`$XDG_CONFIG_HOME/wa/active-profile` — a plain text file containing exactly the profile name followed by a single newline. No TOML, no JSON, no quotes. This matches Docker's `current_context` convention (simpler than kubectl's embedded YAML).

**Read semantics**: if the file does not exist, the active profile falls through to the singleton or default rule. If the file exists but the name does not match a real profile, an error is returned at exit code 78.

**Write semantics**: `wa profile use <name>` writes the file atomically (write `.tmp` then rename). Content is validated before write.

## 8. Profile subcommand tree

The Cobra subcommand tree for `wa profile`:

```
wa profile list             # enumerate profiles with status
wa profile use <name>       # set active profile
wa profile create <name>    # mkdir + seed empty allowlist, NO pairing
wa profile rm <name>        # remove with hard constraints
wa profile show [<name>]    # display metadata for one profile (active by default)
```

Plus a top-level command:

```
wa migrate [--dry-run|--rollback]  # explicit migration trigger (daemon auto-migrates too)
```

## 9. Relationships

```
User
  ├─ selects ─▶ Profile (via flag, env, active-profile file, or "default")
  │
Profile
  ├─ maps to ─▶ PathResolver
  │               ├─ session.db, messages.db, allowlist.toml, audit.log, wad.log (per profile)
  │               ├─ wa.sock, wa.lock (per profile, flat)
  │               └─ pair.html (per profile, in tmp)
  │
  ├─ runs as ─▶ wad process (one process per profile)
  │               ├─ whatsmeow.Adapter (own sqlstore, own client)
  │               ├─ app.Dispatcher (own rate limiter, own warmup, own audit)
  │               └─ socket.Server (own listener, own flock)
  │
  └─ installed as ─▶ service instance (systemd wad@<profile> or launchd com.yolo-labz.wad.<profile>)

Migration
  ├─ detects ─▶ 007 layout (session.db in flat wa/)
  ├─ locks ─▶ existing sqlstore flock
  ├─ moves ─▶ 5 files to default/ subdir
  └─ writes ─▶ schema-version=2, active-profile=default
```

## 10. LoC budget reference

Matches plan.md. Total ~865 LoC (585 production + 280 tests). The data model is lightweight because everything is either a string, a validated path, or a one-shot transaction. No long-lived stateful objects.
