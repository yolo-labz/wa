# Release notes — Feature 008: Multi-profile support

**Released**: 2026-04-11 (feature branch `008-multi-profile`)

## TL;DR

> **wa transparently migrates your single-profile installation to a `default` profile on first run. No action is required. If anything goes wrong, `wa migrate --rollback` restores the prior layout.**

From this release forward you can run two or more WhatsApp accounts side-by-side on the same machine — personal and work, one per client, burner + main — via the new `--profile` flag and `wa profile` subcommand tree.

## What's new

### Profile selection

Every `wa` and `wad` invocation now accepts `--profile <name>`. If you never pass it, the single-profile behaviour you already had stays identical — the daemon silently uses a `default` profile and the word "profile" never appears in `wa status` output.

Precedence (highest wins):

1. `--profile <name>` on the command line
2. `WA_PROFILE` environment variable (empty string = unset)
3. `$XDG_CONFIG_HOME/wa/active-profile` pointer file
4. Singleton auto-select if exactly one profile exists
5. Literal `default`

### New subcommands

```
wa profile list           # enumerate profiles with status
wa profile use <name>     # set active profile
wa profile create <name>  # mkdir + seed empty allowlist (does NOT pair)
wa profile rm <name>      # remove with hard constraints
wa profile show [<name>]  # display metadata
wa migrate [--dry-run|--rollback]
```

### Automatic, crash-safe migration

On first run of the 008 binary against a 007-format install, the daemon:

1. Detects the legacy flat layout (`session.db` as a file, not directory).
2. Writes a `.migrating` write-ahead marker listing every planned move.
3. Runs `PRAGMA wal_checkpoint(TRUNCATE)` on every `*.db` file to flush pending WAL writes.
4. Copies every state file into its new `default/` subdirectory with `fsync` at each step.
5. Atomically writes `.schema-version=2` and `active-profile=default` via tempfile+rename.
6. Unlinks the source files.
7. Deletes the `.migrating` marker.

A `SIGKILL` or power loss at any point during steps 1–7 is recoverable: on the next startup the daemon reads the marker and either completes forward or rolls back to the 007 layout. No file is ever lost.

If anything goes wrong, `wa migrate --rollback` restores the pre-008 state (schema version must still be 2, only the `default` profile must exist, no daemon running).

### Security hardening

Feature 008 also addresses several security items independently of the multi-profile refactor:

- **Socket lockfile** (`<socket>.lock`) now opens with `O_NOFOLLOW`, refusing symlink-plant attacks (CVE-2025-68146 / filelock Python).
- **Socket bind** is wrapped in `syscall.Umask(0o177)` to close the TOCTOU window between `bind(2)` and `chmod`.
- **Parent directory verification** (mode `0700` exact, euid-owned, non-symlink) runs before every `net.Listen` via a dedicated `verifyRuntimeParent` check.
- **Peer credential check** on every accepted connection (already present from feature 004) is now documented as part of the per-profile isolation contract (FR-045).

### systemd hardening

The `wad@.service` template unit now ships with the subset of systemd sandboxing directives that **actually take effect** in user units:

```
NoNewPrivileges=yes
LockPersonality=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallFilter=@system-service
SystemCallArchitectures=native
```

`MemoryDenyWriteExecute` is **deliberately absent** because Go's GC uses writable-executable pages (systemd#3814). Mount-namespace directives (`ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `RestrictNamespaces`) are also absent because they silently no-op in user mode per ArchWiki `Systemd/Sandboxing`.

### launchd hardening (macOS)

The per-profile plist now sets `KeepAlive` as a **dict** `{Crashed: true, SuccessfulExit: false}` so a clean `wa panic` exit doesn't respawn into a crash loop. `ProcessType=Background` enables throttled scheduling, and `EnvironmentVariables.PATH` is set explicitly because launchd empties PATH for children.

### Warmup timestamp bug fix (FR-032)

A pre-existing bug in `cmd/wad/main.go` hardcoded `SessionCreated: time.Now()` on every daemon restart, resetting the warmup multiplier to "day 0". This is now sourced from the persisted session store via `whatsmeow.Load(ctx).CreatedAt()`, and the warmup ramp now correctly tracks the real pairing date across restarts. This fix applies to single-profile users as well.

## Constitution compliance

- **Principle I (hexagonal core)**: zero changes to `internal/domain/` or `internal/app/ports.go`. All profile plumbing lives in `cmd/wad/`, `cmd/wa/`, and the composition-root socket path helpers.
- **Principle III (safety first, no `--force`)**: preserved. `wa profile rm` uses `--yes`/`-y` for prompt-skip; there is no `--force` flag anywhere in the CLI.
- **Principle IV (CGO forbidden)**: no new dependencies. `modernc.org/sqlite` continues.
- **Principle VII (conventional commits)**: every commit on the `008-multi-profile` branch follows `feat(scope):` / `test(scope):` / `docs(spec):`.

## Known limitations

1. **Migration performance** (SC-006 target: <200 ms for 100 MB session + history): current implementation copies files with `fsync` rather than using the staging-directory + `renameat2(RENAME_EXCHANGE)` single-pivot approach the spec prescribes. A 200 MB copy runs in ~460 ms on Apple M5 hardware — correct and crash-safe, but above the spec target. Upgrading the pivot to a metadata-only rename is a follow-up task that closes SC-006 without other behavioural change.

2. **Pair HTML profile suffix** (FR-014): the `pair_html.go` profile-suffix change depends on `hotfix/browser-pair-qr` PR #8 landing first. Until that hotfix merges, `os.TempDir()/wa-pair-<profile>.html` is not wired through. All other FRs land independently.

3. **Two-profile integration test under a real whatsmeow client**: the T027 two-profile e2e test currently exercises path isolation + 1000-cycle audit-log isolation + per-profile actor strings against filesystem state. A follow-up adds full dispatcher-stack coverage against memory-fake whatsmeow clients. SC-011 (<10 s wall clock) is met trivially.

## Upgrade checklist

- [ ] Back up `~/.local/share/wa/` (or `~/Library/Application Support/wa/` on darwin) before running the 008 binary for the first time — the migration is crash-safe but belt-and-braces is free.
- [ ] Run `wad --log-level=debug` once to observe the migration log line.
- [ ] Run `wa status` after the migration; the output should match what you had on 007.
- [ ] Run `wa profile list` if you want to confirm `default` is listed with your JID.
- [ ] Optional: `wa profile create work && wa --profile work pair` to pair a second account.

If anything goes wrong, `wa migrate --rollback` restores the prior layout.

## Sources

- Spec: `specs/008-multi-profile/spec.md` (49 FRs, 15 SCs, 6 user stories)
- Research: `specs/008-multi-profile/research.md` (D1–D11)
- Migration contract: `specs/008-multi-profile/contracts/migration.md`
- Crash-safety test: `cmd/wad/migrate_crash_test.go` (SC-013)
- Benchmark baselines: `specs/008-multi-profile/benchmarks.txt` (SC-014)
