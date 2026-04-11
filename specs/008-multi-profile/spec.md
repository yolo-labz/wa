# Feature Specification: Multi-Profile Support

**Feature Branch**: `008-multi-profile`
**Created**: 2026-04-11
**Status**: Draft
**Input**: User description: "Support multiple WhatsApp accounts simultaneously via per-profile daemon instances. Each profile gets its own session db, allowlist, audit log, rate limiter, socket. Backward compatible with existing single-profile deployments."

## Overview

Today's `wa` daemon is single-tenant: one `wad` process holds one WhatsApp account, one `session.db`, one allowlist, one audit log, one socket. Real users have multiple WhatsApp accounts — personal and work, or one per client — and want to run them from the same machine without resorting to env-variable hacks.

This feature adds a **profile** concept to both binaries. A profile is a named isolation boundary that scopes all per-user state: session database, history database, allowlist, audit log, runtime socket, and warmup timestamp. Each profile runs as its own `wad` process (one daemon per account) with its own complete safety pipeline. The CLI (`wa`) picks which daemon to talk to via `--profile <name>`, with the same sysexits + NDJSON contract as today.

The design is **backward compatible**: a user who never passes `--profile` continues to work exactly as before, and their existing paired session is silently migrated to a `default` profile on first run of the 008 binary. A user who has never heard of profiles types `wa status` after upgrade and sees it work — the word "profile" never appears in output unless they opt in.

The implementation follows a per-daemon-per-profile model (rejected alternative: one multi-tenant daemon holding N clients). Rationale: full process isolation bounds blast radius, zero changes to port interfaces, each profile has its own warmup timestamp and rate limiter naturally. The model matches `gpg-agent --homedir`, which is the closest architectural analog (per-profile agent, per-profile socket, per-profile encrypted store).

All decisions in this spec trace to `specs/008-multi-profile/research.md` (D1–D8) and `specs/008-multi-profile/refactor.md` (surface-area inventory from the 5-agent codebase exploration).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Seamless upgrade for existing single-profile users (Priority: P1)

As a user who paired WhatsApp with the 007 release and runs `wad` on my MacBook every day, I upgrade to the 008 release. I expect **zero friction**: nothing in my workflow changes, my session is not lost, my allowlist is preserved, my audit log is preserved, and `wa status` continues to show my connected account with the same JID as before.

**Why this priority**: Breaking existing users is unacceptable. The migration must be silent, atomic, and reversible. Any failure mode must fail safe (keep legacy paths intact) rather than fail loud.

**Independent Test**: Start with a 007-format data layout (flat `~/.local/share/wa/session.db` etc.), run the 008 `wad` binary, verify:
1. Migration happens on first startup
2. Files are moved under a `default/` subdirectory atomically
3. `wa status` returns the same JID as before migration
4. `wa send --to <self> --body test` delivers a message through the default profile
5. Rollback via `wa migrate --rollback` restores the exact original layout

**Acceptance Scenarios**:

1. **Given** an existing 007 install with a paired session at `~/Library/Application Support/wa/session.db`, **When** the user runs the 008 `wad` binary for the first time, **Then** `wad` atomically moves all state files into `~/Library/Application Support/wa/default/`, writes `active-profile` pointing at `default`, logs one `migrated legacy single-profile layout → default/` audit entry, and starts normally.
2. **Given** the post-migration state, **When** the user runs `wa status` without any flags, **Then** the command returns `connected: true` with the same JID as before the upgrade.
3. **Given** the post-migration state, **When** the user runs `wa migrate --rollback`, **Then** the files return to the pre-008 layout, `active-profile` is deleted, and a subsequent 008 `wad` startup triggers the migration again cleanly.
4. **Given** a 007 install where migration started but was interrupted (e.g., power loss mid-rename), **When** the user re-runs `wad`, **Then** the migration completes idempotently without data loss.
5. **Given** migration fails part-way (e.g., disk full), **When** `wad` exits, **Then** the partial move is rolled back inside the existing flock, leaving the 007 layout intact, and the operator sees a clear error message.

---

### User Story 2 — Add a second profile for a work account (Priority: P1)

As a user who already has a personal WhatsApp paired as the `default` profile, I want to pair my work WhatsApp as a separate profile named `work`. I want `wa --profile work pair` to show me a QR code in the browser, I want to scan it with my work phone, and I want both daemons to run simultaneously without interfering with each other.

**Why this priority**: This is the primary reason the feature exists. Without it, 008 is just a directory-shuffling refactor.

**Independent Test**: With a running `default` daemon already paired, start a second `wad --profile work` in another terminal, verify:
1. The second daemon starts without conflicting with the first (different socket path, different flock, different state dir)
2. `wa --profile work pair` opens the browser QR flow
3. After scanning (manual step), the work profile is paired
4. `wa profile list` shows both profiles with the active one starred
5. `wa --profile default status` and `wa --profile work status` return different JIDs
6. `wa --profile work send --to <work-jid> --body hello` delivers a message through the work account's safety pipeline without affecting the personal account's rate limiter state

**Acceptance Scenarios**:

1. **Given** a running `default` profile daemon, **When** the user starts `wad --profile work` in another terminal, **Then** the second daemon starts successfully with its own socket at `$XDG_RUNTIME_DIR/wa/work.sock`, its own flock at `$XDG_RUNTIME_DIR/wa/work.lock`, and its own state directory at `$XDG_DATA_HOME/wa/work/`.
2. **Given** two running daemons, **When** the user runs `wa profile list`, **Then** output shows two rows: `* default (connected)` and `work (not paired)` with the active profile starred.
3. **Given** the work daemon is not yet paired, **When** the user runs `wa --profile work pair`, **Then** the browser QR flow begins as in feature 007 but writes to `os.TempDir()/wa-pair-work.html` so it does not collide with any concurrent `default` profile pairing.
4. **Given** a successful pair on the work profile, **When** the user runs `wa --profile work status`, **Then** the output shows the work account's JID, which is different from the `default` JID.
5. **Given** both profiles paired, **When** the user calls `wa --profile default send ...` and `wa --profile work send ...` in the same second, **Then** each send consumes a token from its own profile's rate limiter, and both messages deliver without interference.

---

### User Story 3 — Auto-complete and discovery (Priority: P2)

As a user with three profiles (personal, work, testing), I want shell completion on `wa --profile <TAB>` to show me the names of my profiles so I don't have to remember them or type `wa profile list` first.

**Why this priority**: Quality of life. The feature works without it, but typing `wa --profile <TAB>` and getting the correct suggestions is the difference between "I use it" and "I hate it".

**Independent Test**: `wa completion bash > /tmp/wa.bash && source /tmp/wa.bash` in a bash shell, then type `wa --profile <TAB>` and assert the three profile names appear as completions.

**Acceptance Scenarios**:

1. **Given** three profiles exist (`default`, `work`, `testing`), **When** the user types `wa --profile <TAB>` in bash/zsh/fish, **Then** the shell completes with all three profile names.
2. **Given** no profiles exist yet beyond default, **When** the user types `wa --profile <TAB>`, **Then** only `default` is offered.
3. **Given** the user types `wa --profile w<TAB>`, **Then** only `work` is offered.
4. **Given** a profile whose session.db does not exist (mkdir'd but never paired), **When** the user tabs, **Then** the incomplete profile is NOT offered (the completer only lists profiles with a valid `session.db`).

---

### User Story 4 — Active profile pointer and default selection (Priority: P2)

As a user who mostly uses my work profile, I want to run `wa profile use work` once to set my active profile, and then every subsequent `wa` command without `--profile` uses `work` until I change it again. This mirrors `kubectl config use-context` and `docker context use`.

**Why this priority**: Reduces friction for the common case (one primary profile). Users can avoid typing `--profile work` on every command.

**Independent Test**: Set active profile, run `wa status` without flags, verify the expected profile is used. Then switch, verify the other is used.

**Acceptance Scenarios**:

1. **Given** the user has a `default` active profile, **When** they run `wa profile use work`, **Then** the file `$XDG_CONFIG_HOME/wa/active-profile` is written with content `work\n` and subsequent `wa status` (no `--profile` flag) queries the work daemon.
2. **Given** the active profile is `work`, **When** the user runs `wa --profile default status`, **Then** the explicit flag overrides the active file and queries the default daemon instead.
3. **Given** the `active-profile` file is missing, **When** the user runs `wa status` and exactly one profile exists, **Then** that profile is used silently. If multiple profiles exist, the command errors with exit code 78 and a clear message: `multiple profiles exist (default, work); pass --profile or run 'wa profile use <name>'`.
4. **Given** the user sets `WA_PROFILE=work` in the environment, **When** they run `wa status`, **Then** the env var takes precedence over the `active-profile` file (but not over the `--profile` flag).

---

### User Story 5 — Install each profile as its own system service (Priority: P2)

As a user running Linux who wants both profiles to auto-start at login, I want to run `wad install-service --profile default` and `wad install-service --profile work` and have each enable its own systemd user unit via template substitution. Same on macOS with per-profile launchd plists.

**Why this priority**: Without auto-start, the user has to run `wad` manually in a terminal every time. This is fine for development but not for daily use.

**Independent Test**: On a Linux VM or test host, install both profiles as services, verify `systemctl --user list-units 'wad@*'` shows both instances, reboot, verify both daemons auto-start.

**Acceptance Scenarios**:

1. **Given** a Linux system with no systemd user units for `wad`, **When** the user runs `wad install-service --profile default`, **Then** a template file `~/.config/systemd/user/wad@.service` is written (if not already present) and the instance is enabled via `systemctl --user enable --now wad@default.service`.
2. **Given** the template already exists from a prior install, **When** the user runs `wad install-service --profile work`, **Then** the existing template is reused (not overwritten) and only the new instance symlink is added.
3. **Given** two profiles installed as services, **When** the user runs `systemctl --user list-units 'wad@*'`, **Then** both `wad@default.service` and `wad@work.service` are listed as running.
4. **Given** a macOS system, **When** the user runs `wad install-service --profile work`, **Then** a new plist is written at `~/Library/LaunchAgents/com.yolo-labz.wad.work.plist` with label `com.yolo-labz.wad.work` and loaded via `launchctl bootstrap gui/$(id -u) <plist>`.
5. **Given** the user runs `wad uninstall-service --profile work` on either platform, **Then** only the specified profile's service is stopped and its file removed; other profiles remain unaffected.

---

### User Story 6 — Profile lifecycle (create, list, remove) (Priority: P2)

As a user managing multiple profiles, I want `wa profile` subcommands to create, list, and remove profiles without manually mkdir'ing directories or editing config files.

**Independent Test**: Create a profile, list to verify, remove, list to verify gone.

**Acceptance Scenarios**:

1. **Given** no `testing` profile exists, **When** the user runs `wa profile create testing`, **Then** the directory tree is created (`$XDG_DATA_HOME/wa/testing/` etc.), an empty allowlist is seeded, and the user is prompted to run `wa --profile testing pair` to pair a device.
2. **Given** multiple profiles exist, **When** the user runs `wa profile list`, **Then** output is a table with columns `PROFILE  ACTIVE  STATUS  JID` where ACTIVE shows a star for the active profile and STATUS shows `connected`/`not-paired`/`daemon-stopped`.
3. **Given** a profile that is not active and is not the only profile, **When** the user runs `wa profile rm testing --yes`, **Then** the profile's entire state directory tree is removed. Without `--yes`, the command prompts for confirmation. (`-y` is the short form, matching `apt -y` / `dnf -y` convention. Per constitution §III there is no `--force` flag anywhere in the CLI.)
4. **Given** only one profile exists, **When** the user runs `wa profile rm default`, **Then** the command refuses with `cannot remove the only profile`.
5. **Given** the active profile is `work`, **When** the user runs `wa profile rm work`, **Then** the command refuses with `cannot remove active profile; switch first with 'wa profile use <other>'`.

---

### Edge Cases

- **Profile name too long on darwin**: `wad` computes the final socket path length at startup; if `len(socketPath) >= 104` bytes, the daemon refuses to start with a clear error pointing at the `sun_path` limit. This protects users with unusually long home directories.
- **Invalid profile name** (contains `/`, `..`, `@`, `<`, `>`, space, etc.): rejected at flag parse time with exit code 64 and a regex error message.
- **Reserved profile name** (`.`, `..`, `con`, `prn`, `aux`, `nul`, Windows device names, `root`, `system`, `wa`, `wad`): rejected at flag parse time with an "`X is a reserved name`" error.
- **Profile name `default` is ALLOWED**: it's the canonical default profile, not a reserved name.
- **Two daemons race on the same profile**: prevented by the existing `lockedfile.Mutex` on `$XDG_RUNTIME_DIR/wa/<profile>.lock`. Second daemon exits with `ErrAlreadyRunning`.
- **User manually creates `default/` directory before first 008 run**: the migration refuses (it checks `default/` does NOT exist before moving) and logs a warning; the user must either `rmdir default/` or run `wa migrate` manually.
- **Migration partially completes then fails mid-way**: the whole sequence runs inside the existing flock on `session.db.lock`, and filesystem `rename(2)` is atomic per POSIX. If `mkdir -p` succeeds but one of the moves fails, subsequent runs detect the incomplete state and finish it; if `rename` itself fails, the migration is effectively never-started because the source files still exist at their old paths.
- **`wa profile use <nonexistent>`**: errors with `profile X does not exist; available: [list]` at exit code 78.
- **`wa --profile <invalid-regex>`**: errors at flag parse time with exit code 64 and the regex rule printed.
- **Existing pair HTML collision**: the pairing HTML path becomes `os.TempDir()/wa-pair-<profile>.html` to prevent two simultaneous pair flows from overwriting each other.
- **Cache directory is shared**: `$XDG_CACHE_HOME/wa/` (media thumbnails, downloaded files) is profile-less because whatsmeow media is SHA-256 content-addressed and cross-profile sharing is safe. Documented but not exposed as a configurable.
- **`wa profile rm` on a profile whose daemon is still running**: refuses with `daemon is running; stop it first with systemctl/launchctl or by killing PID <x>`.
- **Empty or whitespace-only profile source**: `WA_PROFILE=""`, `WA_PROFILE="  "`, `--profile ""`, or an `active-profile` file containing only whitespace/BOM MUST all be treated as unset (fall through to the next source in the precedence chain). This explicitly closes the aws-cli empty-profile footgun.
- **macOS App Sandbox clients**: a `wa` CLI invocation made from inside an App Sandbox container CANNOT open the `~/Library/Caches/wa/<profile>.sock` unix socket — sandboxed processes are blocked from connecting to non-sandboxed local sockets regardless of filesystem permissions. This is a documented Apple platform limitation (DTS forum 126059, 788364) and an explicit non-goal. Users who want to invoke `wa` from sandboxed tooling must use an unsandboxed terminal.
- **Daemon in the middle of whatsmeow reconnect**: `wa profile list` MUST distinguish `connected` (whatsmeow client `IsConnected() == true`) from `reconnecting` (client exists but not currently connected) from `daemon-stopped` (no RPC response). The `reconnecting` state exists because whatsmeow's built-in reconnect loop can take up to 30 seconds under poor network conditions.
- **Case-insensitive filesystem collision**: on APFS/HFS+ darwin systems, attempting `wa profile create work` when a `Work/` directory already exists at the data root MUST fail with exit code 64 and a message pointing at the existing-cased sibling, before any filesystem mutation happens. Prevents the cross-platform "two profiles, one filesystem entry" confusion.
- **Profile directory created out-of-band with a name the regex would reject**: if a user or a backup restore creates a directory whose name violates FR-002 under `$XDG_DATA_HOME/wa/`, `wa profile list` MUST still list it but with an `(invalid)` marker and MUST hex-escape its raw bytes in the output to prevent ANSI/terminal-escape injection (defensive against CVE-2024-52005 / CVE-2024-33899 -style attacks).

## Requirements *(mandatory)*

### Functional Requirements

#### Profile identity and validation

- **FR-001**: The system MUST accept a profile name via four mechanisms, in precedence order: (1) `--profile <name>` flag on both `wad` and `wa`, (2) `WA_PROFILE` environment variable, (3) `$XDG_CONFIG_HOME/wa/active-profile` file contents, (4) literal string `default` as the final fallback. An **empty string** `WA_PROFILE=""` or a whitespace-only `--profile ""` MUST be treated as **unset** (fall through to the next source), NOT as the literal empty profile name. The `active-profile` file contents MUST be trimmed of leading/trailing whitespace (including BOM) before use; if the trimmed content is empty, it is treated as a missing file. This explicitly closes the aws-cli empty-profile footgun (serverless/serverless#9013, aws/aws-cli#3431).
- **FR-002**: Profile names MUST match the regex `^[a-z][a-z0-9-]{0,30}[a-z0-9]$` (lowercase, 2–32 chars, alpha start, alphanumeric end, hyphens allowed, no other punctuation). The name MUST NOT contain a run of two consecutive hyphens (`--`) to avoid shell-completion ambiguity and to match `git-check-ref-format` hygiene. A name that passes the regex MUST additionally satisfy `path/filepath.IsLocal(name) == true` as a defense-in-depth lexical assertion (caught by a test, not the regex).
- **FR-003**: The system MUST reject a reserved name list at flag parse time, compared **case-insensitively** for future-proofing even though the regex is lowercase-only. The reserved list is: (a) filesystem specials `.`, `..`; (b) Windows device names `con`, `prn`, `aux`, `nul`, `com0`–`com9`, `lpt0`–`lpt9`, `conin$`, `conout$`; (c) project identifiers `wa`, `wad`, `root`, `system`; (d) systemd/logind user-scope reserved names `dbus`, `systemd`, `user`, `session`; (e) **subcommand verb collisions** `list`, `use`, `create`, `rm`, `show`, `new`, `delete`, `current`, `switch`, `all`, `none`, `self`, `me`, `migrate`; (f) **systemd unit-type suffix words** `service`, `socket`, `target`, `timer`, `mount`, `path`, `slice`, `scope`, `device`, `swap`. The name `default` is **explicitly allowed** — it IS the canonical default profile. Rejection emits exit code 64 with the offending name and the rule that was violated.
- **FR-004**: On startup, `wad` MUST compute the final socket path and refuse to start with a clear error if `len(socketPath) + 1` (including the implied NUL terminator) exceeds the platform limit — `< 104` on darwin (`sun_path` = 104 bytes per xnu `<sys/un.h>`), `< 108` on Linux (`sun_path` = 108 bytes per `unix(7)`). The failure is a hard error at exit code 78, not a warning.

#### Path layout

- **FR-005**: The session database MUST live at `$XDG_DATA_HOME/wa/<profile>/session.db`.
- **FR-006**: The history database MUST live at `$XDG_DATA_HOME/wa/<profile>/messages.db`.
- **FR-007**: The allowlist TOML MUST live at `$XDG_CONFIG_HOME/wa/<profile>/allowlist.toml`.
- **FR-008**: The audit log MUST live at `$XDG_STATE_HOME/wa/<profile>/audit.log`.
- **FR-009**: The daemon log (stderr sink when run as a service) MUST live at `$XDG_STATE_HOME/wa/<profile>/wad.log`.
- **FR-010**: The unix socket MUST live at `$XDG_RUNTIME_DIR/wa/<profile>.sock` (Linux) or `~/Library/Caches/wa/<profile>.sock` (macOS) — flat, not nested, so `ls` of the parent directory enumerates running daemons.
- **FR-011**: The single-instance lock file MUST live at `<socket>.lock` — sibling to the socket, per feature 004's existing convention.
- **FR-012**: The cache directory `$XDG_CACHE_HOME/wa/` MUST remain profile-less (shared across profiles) because whatsmeow media is content-addressed.
- **FR-013**: The top-level index files MUST live at `$XDG_CONFIG_HOME/wa/active-profile` (one-line plain text, profile name only) and `$XDG_CONFIG_HOME/wa/.schema-version` (integer, currently `2`).
- **FR-014**: The pairing HTML file MUST become profile-suffixed: `os.TempDir()/wa-pair-<profile>.html` to prevent cross-profile collisions during concurrent pair flows.

#### Backward compatibility and migration

- **FR-015**: On first startup of an 008+ `wad` binary with `--profile` unset, the daemon MUST detect a 007-format layout (presence of `session.db` as a file in `$XDG_DATA_HOME/wa/` and absence of `$XDG_DATA_HOME/wa/default/` directory) and perform an automatic migration. Detection MUST be schema-version-branched: `v1 → migrate`, `v2 → noop`, absent version file → treat as `v1`.
- **FR-016**: The migration MUST be crash-safe under power-loss. Because a sequence of N `rename(2)` calls is **not** atomic as a group, the implementation MUST use a **staging-directory + single-pivot** strategy: (a) acquire flock on `session.db.lock`; (b) write a `$XDG_CONFIG_HOME/wa/.migrating` marker file with the planned moves list, fsynced before any data touches; (c) copy-then-fsync every source file into a `default.new/` staging tree with the correct final layout; (d) fsync every destination file, each parent directory, and the staging tree root; (e) perform **one** pivot: on Linux ≥3.15 use `renameat2(..., RENAME_EXCHANGE)` to swap `default.new` with a (possibly empty) `default` entry; on darwin use a two-step `rename("default", "default.old"); rename("default.new", "default")` recorded in the `.migrating` marker so a crash between steps is recoverable; (f) fsync the parent directory of the pivot (using `F_FULLFSYNC` on darwin, `fsync` on Linux ext4/xfs per `Documentation/filesystems/ext4/journal.rst`); (g) write `.schema-version=2` atomically; (h) delete the `.migrating` marker; (i) unlink sources (`default.old`); (j) release flock. On any failure before step (e) the daemon MUST remove `default.new/` and leave the 007 layout intact. On startup, if the `.migrating` marker is present and `.schema-version` is still 1/absent, the daemon MUST replay or roll back per the marker contents before proceeding.
- **FR-017**: The migration move set MUST enumerate SQLite sidecar files to prevent silent WAL data loss. For every `*.db` file, the transaction MUST (a) issue `PRAGMA wal_checkpoint(TRUNCATE)` on the source database before staging so any pending writes in `*-wal` are flushed into the main DB, and (b) move the main file plus its `-wal` and `-shm` sidecars if they still exist post-checkpoint. The full move set is: `session.db`, `session.db-wal`, `session.db-shm`, `messages.db`, `messages.db-wal`, `messages.db-shm`, `allowlist.toml`, `audit.log`, `wad.log` (if present). Missing sidecars are skipped silently. The transaction MUST assert that no other process holds `session.db` open before beginning (flock on the sibling `.lock` file is insufficient because `modernc.org/sqlite` does not share it; use a `fuser`-equivalent check or require the operator to stop the 007 daemon first).
- **FR-018**: The `.schema-version` write MUST happen **after** the pivot succeeds and MUST itself be atomic (`tempfile → fsync → rename → fsync(parent)`). The `active-profile` file MUST be written with the same atomic-tempfile idiom. Neither write may be interleaved with the file moves — the ordering is: moves complete → pivot → fsync pivot parent → write schema-version → write active-profile → delete `.migrating` marker.
- **FR-019**: The migration MUST append one audit log entry with action `migrate`, decision `ok`, actor `wad:migrate`, detail `legacy single-profile → default/ (schema v1 → v2)`. The audit entry is written **after** the pivot succeeds so a crashed migration does not leave a false success record.
- **FR-020**: The migration MUST be idempotent: running the 008 binary a second time on an already-migrated layout (schema-version == 2) MUST short-circuit before any file touches. Idempotency is asserted via schema-version branching, not via presence/absence of specific files.
- **FR-021**: `wa migrate --dry-run` MUST print the exact set of moves that would happen without acting, and exit 0.
- **FR-022**: `wa migrate --rollback` MUST reverse a migration IF the schema version is still 2 AND no profiles other than `default` exist AND the rollback can restore the exact 007 layout. Otherwise, the command refuses with a clear error.

#### CLI surface

- **FR-023**: `wa --profile <name>` MUST become a persistent global flag, defaulting to the resolution chain in FR-001.
- **FR-024**: `wad --profile <name>` MUST accept the same flag with the same default.
- **FR-025**: `wa profile list` MUST print a table with columns `PROFILE  ACTIVE  STATUS  JID  LAST_SEEN`, where ACTIVE is `*` for the active profile, STATUS is one of `connected`, `reconnecting`, `not-paired`, `daemon-stopped`, JID is the paired account or `(unknown)` if the daemon RPC times out within 200 ms, and LAST_SEEN is the RFC3339 timestamp of the most recent successful daemon ping or `-` if never. Profile names sourced from the filesystem (not from validated flags) MUST be rendered through a control-character strip filter that rejects `[\x00-\x1f\x7f\x80-\x9f]` and ANSI CSI sequences, or renders them via `strconv.Quote`, to prevent terminal escape injection (precedent: CVE-2024-52005 Git sideband injection, CVE-2024-33899 WinRAR hidden-file listing). A profile whose filesystem name fails the regex MUST be listed with a loud `(invalid)` marker and its raw bytes hex-escaped.
- **FR-026**: `wa profile use <name>` MUST write the name to `$XDG_CONFIG_HOME/wa/active-profile`. If the profile does not exist, the command errors at exit code 78.
- **FR-027**: `wa profile create <name>` MUST create the directory tree for the profile, seed an empty allowlist, and print a next-step hint (`run 'wa --profile <name> pair' to pair a device`). It MUST NOT start a daemon. Before creating any directory, the command MUST perform a **case-insensitive collision check** (`os.ReadDir($XDG_DATA_HOME/wa/)` + case-folded compare) and refuse with exit code 64 if a differently-cased sibling already exists (e.g., `Work` when creating `work`). This prevents the APFS/HFS+ case-insensitivity foot-gun that has bitten every cross-platform tool since 2005.
- **FR-028**: `wa profile rm <name>` MUST refuse if `<name>` is the active profile, the only profile, or currently running as a daemon. With `--yes` (short `-y`), it skips the interactive confirmation prompt but still refuses on the hard constraints. Per constitution §III, the flag is named `--yes` not `--force` — "there is no `--force` flag and there will not be one" is absolute; `--yes` is a prompt-skip, not a safety-bypass, and matches `apt -y` / `rm -f` / `dnf -y` precedent for non-interactive confirmation.
- **FR-029**: Cobra shell completion (`wa completion bash|zsh|fish|powershell`) MUST complete profile names for the `--profile` flag and for `wa profile use/rm` arguments. Completion uses `RegisterFlagCompletionFunc` reading `$XDG_DATA_HOME/wa/*/session.db` via `filepath.Glob`.

#### Daemon isolation

- **FR-030**: Each profile's `wad` process MUST hold its own `whatsmeow.Adapter`, its own `app.Dispatcher` (and therefore its own `RateLimiter`, `SafetyPipeline`, `EventBridge`), its own `socket.Server`, its own `slogaudit.Audit`, and its own sqlitestore/sqlitehistory instances.
- **FR-031**: Two daemons running different profiles MUST NOT share any in-process state. A crash in profile A's daemon MUST NOT affect profile B's daemon (process-level isolation).
- **FR-032**: The warmup multiplier MUST be computed per profile using that profile's session creation timestamp. The pre-existing bug in `cmd/wad/main.go` that hardcodes `time.Now()` for the warmup timestamp MUST be fixed as a prerequisite task in feature 008.
- **FR-033**: The audit log for each profile MUST set the audit event's `Actor` field to `wad:<profile>` so entries are unambiguous when two audit logs are compared.

#### Service installation

- **FR-034**: On Linux, `wad install-service --profile <name>` MUST install a systemd template unit at `~/.config/systemd/user/wad@.service` (once) AND enable the instance via `systemctl --user enable --now wad@<name>.service`. The template uses `%i` for the instance name and `ExecStart=<absolute-wad-path> --profile %i` (resolved via the process's own `os.Executable()` at install time). The `[Service]` section MUST include the following hardening directives — the set that **actually takes effect** in user-mode systemd (per ArchWiki Systemd/Sandboxing and systemd.exec(5) 2024–2026):
  - `NoNewPrivileges=yes` (explicit, not implicit in user mode)
  - `LockPersonality=yes`
  - `RestrictRealtime=yes`
  - `RestrictSUIDSGID=yes`
  - `SystemCallFilter=@system-service`
  - `SystemCallArchitectures=native`
  - `Restart=on-failure`, `RestartSec=5s`
  The template MUST **NOT** set `MemoryDenyWriteExecute=yes` (documented-incompatible with Go runtime — systemd issue 3814) and MUST **NOT** attempt `ProtectSystem=strict`, `ProtectHome`, `PrivateDevices`, `PrivateTmp`, or `RestrictNamespaces`, because those directives silently no-op or fail in user units (they require `CAP_SYS_ADMIN` held by the user manager). A comment block at the top of the template MUST explain these absences so a future contributor does not "helpfully" add them.
- **FR-035**: On macOS, `wad install-service --profile <name>` MUST install a plist at `~/Library/LaunchAgents/com.yolo-labz.wad.<name>.plist` with `Label = com.yolo-labz.wad.<name>` and `ProgramArguments = [<wad-path>, "--profile", "<name>"]`, then load it via `launchctl bootstrap gui/$(id -u) <plist>` (launchctl 2.0 syntax; `load`/`unload` are deprecated). The plist MUST:
  - Set `KeepAlive` as a **dict** `{Crashed: true, SuccessfulExit: false}` (NOT a bare `<true/>`), so a clean `wa panic` exit does not respawn into a crash loop.
  - Set `ProcessType = Background` (enables throttled CPU/IO for a long-running non-UI daemon).
  - Set `RunAtLoad = true` so `launchctl bootstrap` actually starts the job.
  - Provide `EnvironmentVariables` with an explicit `PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin` — launchd gives children an empty PATH by default.
  - **NOT** set `LimitLoadToSessionType`, so SSH-session invocations also work.
  - Leave `ThrottleInterval` at the default (10 s), which rate-limits fast-crash loops — desirable for a daemon.
- **FR-036**: `wad uninstall-service --profile <name>` MUST remove only the specified profile's unit/plist and leave other profiles untouched. On Linux, if the template file is the last remaining `wad@*` artifact after disabling, it MAY be removed; otherwise it MUST be retained.
- **FR-037**: `loginctl enable-linger $USER` MUST be recommended (not automatically run) on first `install-service` on Linux, and subsequent installs MUST skip the recommendation. The requirement to run `enable-linger` is current as of systemd 255/256 (2024–2026): no distro has changed the default; user services still terminate on logout without it. The install command MUST print the exact command to run, not a generic hint.
- **FR-038**: Both install and uninstall commands MUST refuse to run as root (existing feature 007 behavior preserved).

#### Disambiguation when active profile is unclear

- **FR-039**: If `wa` is invoked without `--profile`, without `WA_PROFILE`, without an `active-profile` file, AND multiple profiles exist, the command MUST exit with code 78 and a clear message: `multiple profiles exist (<list>); pass --profile or run 'wa profile use <name>'`.
- **FR-040**: If `wa` is invoked without any profile hint AND exactly one profile exists, that profile MUST be used silently (matches Docker's single-context default).
- **FR-041**: If `wa` is invoked without any profile hint AND zero profiles exist, the command MUST use `default` and, if the daemon is not running, error with `daemon not running; start it with 'wad'`.

#### Socket and lockfile security

- **FR-042**: Before calling `net.Listen("unix", ...)`, `wad` MUST verify the socket parent directory (`$XDG_RUNTIME_DIR/wa/` or `~/Library/Caches/wa/`) via `os.Lstat` (which uses `fstatat(..., AT_SYMLINK_NOFOLLOW)` under the hood on Linux and `lstat(2)` on darwin): it MUST exist as a directory, MUST have mode `0700` exactly (no group or other bits), MUST be owned by `os.Geteuid()`, and MUST NOT be a symlink. Any violation is a hard fail at exit code 78. `os.MkdirAll` is not sufficient alone — it honors umask and does not verify ownership.
- **FR-043**: The socket bind MUST be performed atomically to close the TOCTOU window between `bind(2)` and `Chmod`. The daemon MUST either (a) narrow `umask(0177)` for the duration of the listen call and restore it afterwards, or (b) bind inside an already-verified `0700` parent directory that is inaccessible to any other UID. Approach (b) is preferred because it is stable under `os.Chmod` races.
- **FR-044**: The `.lock` sibling file open MUST pass `O_NOFOLLOW` to refuse symlink traversal, per the CVE-2025-68146 fix pattern (filelock/Python, Nov 2025). The existing `rogpeppe/go-internal/lockedfile` package MUST be wrapped or replaced to ensure `O_NOFOLLOW` is set on the underlying `os.OpenFile` call.
- **FR-045**: On every accepted connection, `wad` MUST check the peer credentials via `SO_PEERCRED` on Linux (captured at connect time, immutable) or `LOCAL_PEEREPID`/`getpeereid(2)` on darwin, and reject with JSON-RPC error code `-32000` if the peer's effective UID does not equal `os.Geteuid()`. This is belt-and-braces defense over `0600` permissions and protects against misconfigured parent directories.
- **FR-046**: The daemon MUST audit-log the peer UID and PID on **every accept** (not just mutating calls), with a dedicated `accept` action. This provides forensic traceability if the socket is ever mis-permissioned or if a same-UID compromise is suspected.
- **FR-047**: Stale-socket cleanup at startup MUST be lock-guarded: the daemon MUST (1) acquire the `.lock` file exclusively, (2) `unlink` the stale socket file if present, (3) `bind` the new socket. An unguarded `unlink` + `bind` sequence permits two racing daemons to both succeed at unlink and then both fail at bind in an order-dependent way.
- **FR-048**: Every path-join operation inside `wad` and `wa` that incorporates a profile name MUST pass through `path/filepath.IsLocal(name) == true` as a defense-in-depth lexical assertion, even though FR-002 guarantees the regex already blocks traversal. The data directory SHOULD be opened via `os.Root`/`os.OpenRoot` for symlink-traversal resistance. The `go.mod` toolchain pin MUST match the repository-wide floor (currently `go 1.25`) and MUST NOT be downgraded below `1.24` because CVE-2026-32282 (`Root.Chmod` race) is fixed only in Go 1.24.x via `fchmodat2`.
- **FR-049**: Profile names MUST NEVER be string-interpolated into shell command strings. They MUST be passed as a separate `argv` element to `os/exec.Command`. A project-wide lint rule (grep for `exec.Command.*fmt.Sprintf.*profile`) MUST enforce this at CI time.

### Key Entities

- **Profile**: A named isolation boundary. Has a unique name (matching FR-002), a state directory tree (FR-005..FR-009), a socket path (FR-010), and an optional service installation (FR-034, FR-035).
- **Active profile pointer**: A one-line file at `$XDG_CONFIG_HOME/wa/active-profile` containing the name of the profile to use when no explicit selector is provided.
- **Schema version**: An integer file at `$XDG_CONFIG_HOME/wa/.schema-version` recording the current on-disk layout version (`2` for feature 008, `1` for the pre-008 implicit single-profile layout). Used by `wa migrate` to detect legacy layouts.
- **Profile registry**: The set of profiles is derived at runtime by globbing `$XDG_DATA_HOME/wa/*/session.db`. No sidecar registry file is kept — filesystem state is authoritative.
- **Migration transaction**: A staging-directory + single-pivot transaction gated by a `.migrating` write-ahead marker. The source files are checkpointed (via `PRAGMA wal_checkpoint(TRUNCATE)` for SQLite DBs), copied into `default.new/`, fsynced, then pivoted via a single `renameat2(RENAME_EXCHANGE)` on Linux ≥3.15 or a two-step rename recorded in the marker on darwin. Startup recovery reads the marker and either completes forward or rolls back. Crash-safe under `SIGKILL` / power loss at every step. See `contracts/migration.md` for the full 25-step sequence.
- **Migration marker**: A write-ahead log file at `$XDG_CONFIG_HOME/wa/.migrating` containing the planned move list + timestamp, written and fsynced before any data touches. Used by startup recovery to distinguish "pre-migration", "mid-migration", and "post-migration" states.
- **Service instance**: A single profile's systemd instance (`wad@<name>.service`) or launchd plist (`com.yolo-labz.wad.<name>`).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An existing 007-format install upgrades to 008 with **zero data loss**, verified by a test that pairs under 007, restarts under 008, and asserts the same JID + allowlist + audit log entries.
- **SC-002**: A fresh 008 install with no prior state creates a `default` profile on first use and the word "profile" never appears in `wa status` output for single-profile users.
- **SC-003**: Two daemons running different profiles sustain 1000 sequential request/response cycles each without cross-contamination of rate limiter state (tested via bench + memory fake).
- **SC-004**: `wa profile list` runs in under 100 ms even with 20 profiles (stress-tested).
- **SC-005**: Profile name validation runs at flag-parse time in under 1 ms (unit test with table-driven regex cases).
- **SC-006**: The migration from 007 to 008 completes in under 200 ms for a 100 MB session + history database pair (atomic renames on the same filesystem are metadata operations).
- **SC-007**: `wa migrate --rollback` correctly reverses the migration on 100% of test cases (table-driven rollback test).
- **SC-008**: Shell completion (`wa --profile <TAB>`) returns correct profile names in under 50 ms for up to 50 profiles.
- **SC-009**: `go test -race ./...` passes with zero race warnings after the refactor.
- **SC-010**: `golangci-lint run ./...` reports zero findings; both `core-no-whatsmeow` and `app-no-adapters` depguard rules continue to pass.
- **SC-011**: The two-profile end-to-end test (start two daemons, pair both, send from each, verify isolation) completes in under 10 seconds wall clock.
- **SC-012**: Zero port interface changes in `internal/app/ports.go` — the refactor is entirely in the composition root and path-resolution layer.
- **SC-013**: Migration crash-safety is verified by a **fault-injection test** that kills the migration process (`SIGKILL`) between every pair of steps in the sequence (stage-copy, fsync, pivot, schema-version write, marker delete) and asserts that restart either completes the migration cleanly or rolls it back to the original 007 layout with zero data loss. `testing/synctest` is NOT used for this test — it virtualises time, not the filesystem; the test instead operates on a real `t.TempDir()` with `exec.Command` subprocess kills.
- **SC-014**: SC-004 / SC-006 / SC-008 thresholds are backed by a reproducible benchmark harness committed to the repo (not aspirational targets): `go test -bench=. -run=^$ ./cmd/wad/... -benchtime=10x` with three distinct fixtures — `BenchmarkProfileList` at 20 profiles (SC-004), `BenchmarkCompletion` at 50 profiles (SC-008), and `BenchmarkMigration` at 2 × 100 MB databases (SC-006). The three axes are independent; results are committed to `specs/008-multi-profile/benchmarks.txt`.
- **SC-015**: The `path/filepath.IsLocal` defense-in-depth assertion (FR-048) is exercised by a property test: for every `s` matching the regex in FR-002, assert `filepath.IsLocal(s) && strings.ToLower(s) == s && !strings.Contains(s, "--")`.

## Threat Model

This feature's security posture is **same-user only**. The threat model assumes:

- **In scope — defended against**:
  1. A malicious WhatsApp contact sending crafted messages that try to inject through `wa` into Claude Code (mitigated by `<channel>` wrapper in feature 007 and allowlist in feature 005).
  2. Path-traversal or argument-injection attempts via the profile name (mitigated by FR-002 regex + FR-048 `IsLocal` + FR-049 argv-only interpolation).
  3. TOCTOU races on the socket bind path or lockfile open (mitigated by FR-042 parent verification + FR-043 atomic bind + FR-044 `O_NOFOLLOW`).
  4. Silent data loss under power-loss during migration (mitigated by FR-016 staging+pivot + FR-017 WAL checkpoint + FR-018 fsync ordering).
  5. Terminal-escape injection through profile names printed by `wa profile list` (mitigated by FR-025 output escaping).
  6. Cross-profile state contamination (mitigated by FR-030/FR-031 process isolation).
  7. A local unprivileged process attempting to connect to a wrongly-permissioned socket (mitigated by FR-045 peer-credential check + FR-046 accept audit logging).

- **Out of scope — NOT defended against**:
  1. A compromised process running **as the same UID** as `wad`. Such a process can read `~/.local/share/wa/default/session.db`, impersonate the daemon, or read the audit log. FileVault / LUKS / dm-crypt is the documented protection boundary against offline disk attack.
  2. Root on the local system. Root can trivially read any file, attach to any process, and read any socket.
  3. A malicious package replacing the `wa` or `wad` binary on disk. Distribution integrity (Homebrew signatures, Nix store hashes, GoReleaser checksums) is the protection against this — not anything in this feature.
  4. Side-channel attacks against the SQLite store (timing, cache, fault injection). whatsmeow's ratchet design is the upstream boundary.
  5. WhatsApp server-side account compromise. Outside every defense in this repo.
  6. Users who deliberately misconfigure permissions (`chmod 0777 ~/.local/share/wa`). Documentation is the only mitigation.
  7. Headless multi-user Linux hosts where `$XDG_RUNTIME_DIR` is misconfigured or shared between UIDs. FR-042 detects this and refuses to start, but does not "fix" it.

This threat model is deliberately narrow because `wa` is a **single-user personal automation tool**, not a multi-tenant service. Attempting to defend against a same-UID compromise would require ring-3 isolation mechanisms (sandbox-exec, user namespaces, seccomp-bpf) that are incompatible with the Go runtime, launchd user agents, and systemd user units. The feature's primary defense is process isolation between profiles (blast-radius bounding), not sandboxing against the local user.

## Assumptions

- Each additional profile consumes approximately **30 MB RSS** (the `wad` binary baseline). Running 5 profiles = ~150 MB total. Documented as a trade-off for process-level isolation.
- The cache directory `$XDG_CACHE_HOME/wa/` remains **shared** across profiles because whatsmeow media is SHA-256 content-addressed. Profiles do not leak information through shared cache because the cache is keyed by content hash, not by profile.
- `loginctl enable-linger` is run **manually** by the user after the first `install-service` on Linux. The install command prints a hint but does not execute the command.
- Profile names are **case-sensitive in the regex** but all lowercase by rule, so in practice they're case-insensitive.
- The existing `flock` on `session.db.lock` provides **advisory** serialisation between concurrent `wad`/`wa migrate` invocations, but is **NOT sufficient on its own** because `modernc.org/sqlite` does not share that flock — the migration pre-condition therefore also runs a `fuser`/procfs check (Linux) or `lsof` check (darwin) to assert no process has `session.db` open before proceeding. Both layers are required. See FR-017 and contracts/migration.md §Pre-conditions.
- Users running the 008 binary while a 007 daemon is still holding the flock will see the 008 daemon refuse to start until the 007 daemon is stopped. This is intentional.
- On first run of 008 after an upgrade, the migration is **silent but logged** — one audit entry is sufficient; no interactive prompt.
- The `app-dispatcher` layer is entirely profile-unaware by design. Profiles exist only in the composition root (`cmd/wad`) and the CLI root (`cmd/wa`).
- The pre-existing warmup timestamp bug (FR-032) is fixed as part of feature 008 and is NOT a separate feature. This keeps the fix bundled with the feature that exposes it.

## Dependencies

- **Feature 002 (domain-and-ports)** — port interfaces unchanged. Zero changes to `internal/app/ports.go`.
- **Feature 003 (whatsmeow-adapter)** — the adapter's `GetFirstDevice()` call already works per profile because each profile gets its own sqlstore path. Zero adapter changes.
- **Feature 004 (socket-adapter)** — socket path resolver gains a `PathFor(profile string)` sibling function. Existing `Path()` remains for backward compat.
- **Feature 005 (app-usecases)** — dispatcher is profile-unaware; each per-profile daemon constructs its own. Zero changes.
- **Feature 006 (binaries-wiring)** — the composition root in `cmd/wad/main.go` is the primary refactor target.
- **Feature 007 (release-packaging)** — service installation logic is extended to be profile-aware. GoReleaser and Nix flake are unchanged (binaries are profile-neutral at build time).
- **Hotfix branch `hotfix/browser-pair-qr` (PR #8)** — the pairing HTML path change in FR-014 depends on PR #8 landing first so the concurrent-pair-flow collision fix can be added.

## Out of Scope

- **Multi-tenant single daemon** (one `wad` process holding N whatsmeow clients). Rejected in research D1 for blast-radius reasons. May be revisited if resource overhead becomes a real constraint.
- **Profile-level config knobs** beyond state isolation (e.g., per-profile rate limits, per-profile warmup overrides). The rate limiter stays hardcoded at 2/s, 30/min, 1000/day per profile. Per-profile tuning is a future feature.
- **Cross-profile operations** (e.g., "send from both profiles atomically", "mark read across all profiles"). Each `wa` invocation targets exactly one profile. Cross-profile workflows are scriptable by the user via shell loops.
- **Profile sharing between machines** (e.g., syncing profiles via Dropbox/rsync). The user is responsible for their own data portability. The file layout is simple enough to `rsync` manually but no automation is provided.
- **Per-profile whatsmeow client configuration** beyond what the daemon already exposes. Each profile uses the same whatsmeow defaults.
- **Profile encryption at rest**. The session database remains plaintext (protected by filesystem permissions); FileVault/LUKS is the documented boundary.
- **Web UI for profile management**. The CLI and filesystem state are the only interfaces.
- **Windows support**. Not in scope per CLAUDE.md; reserved profile name list future-proofs for a potential port.

## Notes

- This feature is derived from three parallel agent swarms:
  - **Swarm 1 (5 agents)** mapped the codebase and produced `specs/008-multi-profile/refactor.md`
  - **Swarm 2 (5 agents)** researched prior art and produced `specs/008-multi-profile/research.md` D1–D8
  - **Swarm 3 (5 agents, 2026-04-11 checklist review)** produced D9 (UDS security posture), D10 (systemd user-unit hardening), and D11 (name-validation CVE posture). Swarm 3 also surfaced FR-042..FR-049 and the migration WAL/SHM + staging-pivot corrections.
- All three documents are inputs to this spec and must be consulted during `/speckit:plan`.
- The bundled pre-existing bug fix (warmup timestamp) is flagged as a prerequisite task in feature 008's tasks.md, not as a separate pre-feature PR, because it gates meaningful testing of per-profile warmup state.
