# Contract: Migration Transaction

**Feature**: 008-multi-profile
**Status**: Revised 2026-04-11 (post-swarm-3 review)

This contract defines the transaction protocol that upgrades a pre-008 single-profile install to the 008 per-profile layout. It is bound by crash-safety, idempotency, and rollback guarantees under power-loss, `SIGKILL`, and disk-full scenarios.

## CRITICAL CORRECTIONS FROM EARLIER DRAFTS

Two defects in the original contract were identified and corrected:

1. **A sequence of N `rename(2)` calls is NOT atomic as a group.** POSIX guarantees atomicity of a single rename, not of a sequence. A crash between renames leaves a half-migrated filesystem that is indistinguishable from pre- or mid-migration. The corrected design uses a **staging directory + single pivot** pattern, with a persistent marker file for recovery.
2. **SQLite WAL/SHM sidecar files were omitted from the move set.** `modernc.org/sqlite` with `journal_mode=WAL` (the default) maintains `session.db-wal` and `session.db-shm` alongside the main file. Moving `session.db` without these sidecars results in **silent data loss of any committed-but-not-checkpointed transactions** ([SQLite WAL docs §Backwards Compatibility](https://www.sqlite.org/wal.html)). The corrected sequence issues `PRAGMA wal_checkpoint(TRUNCATE)` before staging and moves the sidecars with the main file.

Both corrections are reflected in the FRs (FR-016, FR-017) and the sequence below.

## When the migration runs

On **every** `wad --profile <anything>` startup the daemon checks:

1. Does `$XDG_CONFIG_HOME/wa/.migrating` exist? → **Enter recovery mode** (see §Recovery) BEFORE any schema-version check.
2. Does `$XDG_CONFIG_HOME/wa/.schema-version` exist and equal `"2"`? → **Skip migration** (already done).
3. Does `$XDG_DATA_HOME/wa/session.db` exist as a regular file (not a directory)? → **Migration candidate**.
4. Does `$XDG_DATA_HOME/wa/default/` exist as a directory? → **Skip migration + warn** (operator must resolve manually or run `wa migrate` explicitly).

If all conditions for "migration candidate" are met AND `--profile` is unset (or equals `default`), the migration runs automatically.

Explicit invocations:
- `wa migrate` — runs the transaction outside of daemon startup
- `wa migrate --dry-run` — prints the planned moves without acting
- `wa migrate --rollback` — reverses a completed migration (under strict pre-conditions)

## Pre-conditions

Before the transaction begins, the implementation MUST assert:

1. **No other process holds `session.db` open.** `flock()` on `session.db.lock` is NOT sufficient — `modernc.org/sqlite` does not share that lock with the daemon. Use a `fuser`-equivalent check (on Linux: `lsof` / read `/proc/*/fd/*`; on darwin: `lsof`) or require the operator to stop the 007 daemon before migration.
2. **Cross-filesystem pre-flight (EXDEV).** Compare `statx`/`syscall.Stat_t.Dev` of the source parent (`$XDG_DATA_HOME/wa/`) and the destination parent (the parent that will hold `$XDG_DATA_HOME/wa/default/`). If they differ, abort with a typed `ErrCrossFilesystem` error at exit code 78 before any file is touched. A rename across filesystems returns `EXDEV`; attempting it and failing mid-sequence is worse than refusing up front.
3. **Free-space check.** Ensure the destination has at least `2 × sum(source file sizes)` bytes free (headroom for the staging copy). If not, abort at exit code 78.
4. **Ownership check.** The source files must be owned by `os.Geteuid()`. Otherwise abort.
5. **`.migrating` marker absence.** If a marker file exists, enter recovery mode first; do not start a fresh migration on top of an interrupted one.

## Crash-safety guarantee

The migration uses the **write-ahead marker + staging + single-pivot + fsync-everywhere** pattern, the same discipline git's `lockfile.c`, etcd's backend migrations, and SQLite's journal mode use.

**Atomicity layers**:

1. **Advisory serialisation**: `flock()` on `$XDG_DATA_HOME/wa/session.db.lock` (the existing 007 lock) prevents two `wad` or `wa migrate` processes from starting a migration concurrently. On process crash, the kernel releases the lock.
2. **Write-ahead marker**: `$XDG_CONFIG_HOME/wa/.migrating` contains a TOML record of the planned moves plus a timestamp. Written and `fsync`ed **before any data is touched**. This converts startup recovery from an observation problem into a log-replay problem.
3. **Staging directory**: all copies land in `$XDG_DATA_HOME/wa/default.new/` (and equivalents under config/state). The sources are untouched until the pivot.
4. **Single pivot**:
   - **Linux ≥3.15**: `renameat2(wa, "default.new", wa, "default", RENAME_EXCHANGE)` — atomic swap in a single syscall.
   - **darwin or Linux < 3.15**: two-step `rename("default", "default.old"); rename("default.new", "default")`, recorded in the marker so a crash between the two renames is recoverable.
5. **Durability**: every copied file, every parent directory, and the `.schema-version` file is `fsync`ed. On darwin, `fcntl(F_FULLFSYNC)` is used — plain `fsync` is documented as insufficient for power-loss durability on APFS ([Apple `fsync(2)` man page](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/fsync.2.html)).

## Canonical sequence

```
Step  Operation                                               Crash-recoverable?
----  ----------------------------------------------------  -------------------
 1    Acquire flock on session.db.lock                       kernel-released
 2    Pre-flight: EXDEV check, free-space, ownership         no side effects
 3    PRAGMA wal_checkpoint(TRUNCATE) on session.db          idempotent
 4    PRAGMA wal_checkpoint(TRUNCATE) on messages.db         idempotent
 5    Close both DB handles                                  idempotent
 6    Write .migrating marker (tmpfile→fsync→rename)        → recovery will replay
 7    fsync($XDG_CONFIG_HOME/wa/)                            (marker durable)
 8    mkdir default.new/ in each of data/config/state        idempotent on re-run
 9    Copy session.db + -wal + -shm → data/default.new/      → remove default.new/ on abort
10    Copy messages.db + -wal + -shm → data/default.new/     → same
11    Copy allowlist.toml → config/default.new/              → same
12    Copy audit.log → state/default.new/                    → same
13    Copy wad.log (if exists) → state/default.new/          → same
14    fsync each copied file                                 durability
15    fsync each default.new/ directory                      durability
16    PIVOT:
      - Linux: renameat2(RENAME_EXCHANGE) default.new↔default
      - darwin: rename(default→default.old); rename(default.new→default)
                                                             → recovery reads marker
17    fsync pivot parent directory                           durability (F_FULLFSYNC on darwin)
18    Atomic write .schema-version=2 (tmpfile→rename+fsync)  → recovery notices v2
19    fsync $XDG_CONFIG_HOME/wa/                             durability
20    Atomic write active-profile=default                    → recovery notices
21    Append audit log entry (migrate/ok)                    idempotent via re-run check
22    Unlink default.old/ (if darwin two-step used)          idempotent
23    Unlink source files (session.db, etc.)                 idempotent
24    Unlink .migrating marker                               idempotent
25    Release flock                                          kernel-released on crash
```

**Failure-mode guarantee**: a `SIGKILL` or power-loss at any step leaves the filesystem in a state recoverable to either "pre-migration (clean)" or "post-migration (clean)" on next startup. Half-migrated is never a final state — the `.migrating` marker forces recovery before any other action.

## Recovery

On startup, if `$XDG_CONFIG_HOME/wa/.migrating` exists, the daemon enters recovery mode:

1. Acquire `flock()` on `session.db.lock`.
2. Read the marker file to learn the planned moves and the pivot step reached.
3. Branch:
   - **Steps 1–15 incomplete**: abort migration by removing `default.new/` directories and unlinking the marker. Original layout is untouched.
   - **Step 16 partial (darwin two-step)**: complete the rename — if `default.new/` still exists, rename it to `default/`. If `default.old/` still exists, unlink it.
   - **Steps 17–24 incomplete but pivot succeeded**: complete the remaining operations (schema-version write, active-profile write, audit log append, source unlinks, marker delete).
4. Release flock.

Recovery MUST be idempotent — running it on an already-recovered layout is a no-op.

## Files migrated (full enumeration)

| # | From | To |
|---|---|---|
| 1 | `$XDG_DATA_HOME/wa/session.db` | `$XDG_DATA_HOME/wa/default/session.db` |
| 2 | `$XDG_DATA_HOME/wa/session.db-wal` (if exists post-checkpoint) | `$XDG_DATA_HOME/wa/default/session.db-wal` |
| 3 | `$XDG_DATA_HOME/wa/session.db-shm` (if exists post-checkpoint) | `$XDG_DATA_HOME/wa/default/session.db-shm` |
| 4 | `$XDG_DATA_HOME/wa/messages.db` | `$XDG_DATA_HOME/wa/default/messages.db` |
| 5 | `$XDG_DATA_HOME/wa/messages.db-wal` | `$XDG_DATA_HOME/wa/default/messages.db-wal` |
| 6 | `$XDG_DATA_HOME/wa/messages.db-shm` | `$XDG_DATA_HOME/wa/default/messages.db-shm` |
| 7 | `$XDG_DATA_HOME/wa/session.db.lock` | **discarded** — 008 lock lives alongside the socket |
| 8 | `$XDG_CONFIG_HOME/wa/allowlist.toml` | `$XDG_CONFIG_HOME/wa/default/allowlist.toml` |
| 9 | `$XDG_STATE_HOME/wa/audit.log` | `$XDG_STATE_HOME/wa/default/audit.log` |
| 10 | `$XDG_STATE_HOME/wa/wad.log` (if exists) | `$XDG_STATE_HOME/wa/default/wad.log` |

Missing source files are skipped silently. Sidecar WAL/SHM files that are empty post-checkpoint may not exist on disk — that is normal and does not indicate an error.

## Post-migration state

After step 24 completes:

1. `$XDG_CONFIG_HOME/wa/.schema-version` exists and contains `2\n`.
2. `$XDG_CONFIG_HOME/wa/active-profile` exists and contains `default\n`.
3. `$XDG_DATA_HOME/wa/default/session.db` is the sole session database, with empty or absent WAL/SHM sidecars.
4. Original 007 paths are unlinked.
5. `.migrating` marker is deleted.
6. One audit log entry appended with action `migrate`, actor `wad:migrate`, decision `ok`, detail `legacy single-profile → default/ (schema v1 → v2)`.

## Idempotency

- Schema-version-branched: `v2` → noop at step 2; `v1` or absent → migration candidate.
- `.migrating` present → recovery first, then re-evaluate.
- Running `wa migrate` after a successful migration exits 0 with a message `already at schema v2`.

## Rollback

`wa migrate --rollback` reverses a completed migration if and only if ALL pre-conditions hold:

1. `$XDG_CONFIG_HOME/wa/.schema-version` exists and equals `"2"`.
2. `ls $XDG_DATA_HOME/wa/` lists exactly ONE directory named `default` (no other profiles created post-migration).
3. The `default/` directory contains the expected files (`session.db`, `messages.db`).
4. The active profile pointer equals `default`.
5. No daemon is currently running (checked by attempting to acquire `default.lock`).
6. No `.migrating` marker exists.

If any pre-condition fails, rollback refuses with a specific error per violation at exit code 78.

If all pre-conditions hold, rollback reverses the migration using the same staging+pivot discipline (mirror of the forward sequence), with its own `.migrating-rollback` marker. The rollback reverses steps 23 → 2 in order, recreating WAL/SHM sidecars as empty stub files if the forward migration had checkpointed them.

## Audit log preservation

**The audit log is moved, not truncated.** After migration, `default/audit.log` contains all pre-008 entries plus the one new `migrate` entry. No gap, no reset.

## Dry-run output format

`wa migrate --dry-run` prints a tabular summary to stdout showing planned moves, EXDEV status, and free-space check result:

```
Migration plan (schema v1 → v2):

Pre-flight:
  Cross-filesystem check:      OK  (source and destination on /dev/disk3s1)
  Free-space check:            OK  (420 MB required, 87 GB available)
  Ownership check:             OK  (all sources owned by uid 501)
  SQLite WAL checkpoint:       will run before staging
  Staging directory:           $XDG_DATA_HOME/wa/default.new/

FROM                                      TO
--------------------------------------    --------------------------------------
~/.local/share/wa/session.db              ~/.local/share/wa/default/session.db
~/.local/share/wa/session.db-wal          ~/.local/share/wa/default/session.db-wal
~/.local/share/wa/session.db-shm          ~/.local/share/wa/default/session.db-shm
~/.local/share/wa/messages.db             ~/.local/share/wa/default/messages.db
~/.local/share/wa/messages.db-wal         ~/.local/share/wa/default/messages.db-wal
~/.local/share/wa/messages.db-shm         ~/.local/share/wa/default/messages.db-shm
~/.config/wa/allowlist.toml               ~/.config/wa/default/allowlist.toml
~/.local/state/wa/audit.log               ~/.local/state/wa/default/audit.log
~/.local/state/wa/wad.log                 ~/.local/state/wa/default/wad.log

After migration:
  Schema version:  2
  Active profile:  default
  Profile count:   1

Run 'wa migrate' (without --dry-run) to apply.
```

Exit code: 0. No files are touched.

## Error handling

| Error | Exit code | Recovery action |
|---|---|---|
| Source file missing mid-transaction | 1 | Remove `default.new/`; unlink marker; leave original |
| Destination `default.new/` already exists | 1 | Refuse; direct user to run recovery |
| `flock` acquisition fails | 10 | No changes |
| Schema version already 2 | 0 | Noop, exit cleanly |
| EXDEV detected at pre-flight | 78 | Refuse with message; no changes |
| Free space below threshold | 78 | Refuse; no changes |
| Ownership check fails | 78 | Refuse; no changes |
| Another process holds DB open | 10 | Refuse; direct user to stop daemon |
| `.migrating` marker from previous run | n/a | Enter recovery mode first |
| Rollback pre-condition violated | 78 | No changes |

## Test coverage requirement

1. **Forward migration unit test**: every row in the "Files migrated" table MUST have a test exercising the copy + pivot + unlink.
2. **Rollback negative tests**: every rollback pre-condition MUST have a test that fails the pre-condition and asserts refusal.
3. **Crash-injection test (SC-013)**: a subprocess-based test that `SIGKILL`s the migration process between every pair of numbered steps in the sequence. Uses `t.TempDir()` + a helper binary under `cmd/wad/internal/migratefault/`. Asserts that startup recovery restores the layout to either "pre-migration clean" or "post-migration clean" with zero data loss. **Explicitly does NOT use `testing/synctest`** (virtualises time only, not filesystem operations).
4. **EXDEV test**: uses `bind mount` (Linux) or symlinks (darwin) to simulate cross-filesystem source/dest and asserts refusal at pre-flight.
5. **WAL checkpoint test**: seeds a `session.db` with pending WAL writes, runs migration, asserts the WAL data is present in the destination main DB (not lost).
6. **Golden tests**: `wa migrate --dry-run` output is pinned via a testscript golden file.

## Primary-source citations

- [POSIX rename(2)](https://man7.org/linux/man-pages/man2/rename.2.html) — single-call atomicity, `EXDEV`
- [Linux renameat2(2)](https://man7.org/linux/man-pages/man2/renameat2.2.html) — `RENAME_EXCHANGE` semantics (Linux ≥3.15)
- [SQLite WAL documentation](https://www.sqlite.org/wal.html) — sidecar files, backwards compatibility
- [SQLite atomic commit](https://www.sqlite.org/atomiccommit.html) — crash-recovery model
- [Linux kernel ext4 journal docs](https://www.kernel.org/doc/html/latest/filesystems/ext4/journal.html) — fsync requirement for rename durability
- [LWN 457667 "Ensuring data reaches disk"](https://lwn.net/Articles/457667/) — Jeff Moyer, 2011, still current
- [Apple `fsync(2)` man page](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/fsync.2.html) — `F_FULLFSYNC` on APFS
- [git lockfile.c](https://github.com/git/git/blob/master/lockfile.c) — reference lock+rename pivot pattern
- [etcd backend migration](https://github.com/etcd-io/etcd/tree/main/server/storage/backend) — write-ahead migration log pattern
