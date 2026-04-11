---
description: "Task list for feature 008 — multi-profile support"
---

# Tasks: Multi-Profile Support

**Input**: Design documents from `/specs/008-multi-profile/`
**Prerequisites**: plan.md, spec.md (49 FRs + 15 SCs), research.md (D1–D11), data-model.md, contracts/ (profile-paths.md, migration.md, service-templates.md), quickstart.md, checklists/requirements.md (49/49 pass), checklists/research.md (50/50 pass)

**Tests**: Tests ARE requested for this feature. Every user story has tests alongside implementation, following the existing test pattern in features 002–007 (unit + contract + integration). Crash-safety of the migration is mandated via a fault-injection test per SC-013.

**Organisation**: Tasks are grouped by user story so each story can be implemented and tested independently.

## Scope note

The spec has 49 functional requirements and 6 user stories. At 38 tasks this list exceeds the CLAUDE.md rule-6 soft cap of ~25 items. The feature is intrinsically larger than the cap because it bundles a crash-safe migration transaction, a CLI subcommand tree, and profile-aware service installation into one PR (each is ~10 tasks on its own). Splitting is possible (e.g., "008a migration" + "008b profile tree"), but the migration transaction depends on the PathResolver from 008a and the service installation from 008b, so a split adds coordination overhead for minimal benefit. Proceeding as one feature; reviewers should expect a larger-than-average PR.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1..US6). Setup/Foundational/Polish tasks have no story label.
- Include exact file paths in descriptions

## Path Conventions

Go module at repo root, hexagonal layout per `CLAUDE.md §Repository layout`. All new files live in `cmd/wad/`, `cmd/wa/`, `internal/adapters/primary/socket/`, or `internal/adapters/secondary/whatsmeow/`. Zero changes to `internal/domain/` or `internal/app/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Per-profile path resolution + profile name validation — the primitives every subsequent task depends on.

- [X] T001 Create `cmd/wad/profile.go` with `ValidateProfileName`, regex `^[a-z][a-z0-9-]{0,30}[a-z0-9]$` + no-`--` rule (FR-002), case-insensitive reserved-name list including subcommand verbs and unit-type suffix words (FR-003), and `PathResolver` struct with all per-profile path methods from data-model §2 (SessionDB, HistoryDB, AllowlistTOML, AuditLog, WadLog, SocketPath, LockPath, PairHTMLPath, CacheDir, ActiveProfileFile, SchemaVersionFile, DataDir, ConfigDir, StateDir, SocketParentDir).
- [X] T002 [P] Create `cmd/wad/dirs.go` with `ensureDirs(profile string) error` that creates per-profile `DataDir`, `ConfigDir`, `StateDir` with explicit `Mkdir(path, 0700)` (not `MkdirAll`), then re-verifies mode/ownership/non-symlink via `Lstat` (FR-042). Socket parent dir handled in T006.
- [X] T003 [P] Extend `internal/adapters/primary/socket/path_linux.go` and `internal/adapters/primary/socket/path_darwin.go` with a `PathFor(profile string) (socketPath, lockPath string, err error)` function that computes the sibling lock path and enforces the `sun_path` budget (`len(socketPath) + 1 <= 104` darwin / `<= 108` Linux) as a hard error (FR-004).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Security primitives, bug fixes, and lint rules that every user story depends on. No user story work can begin until this phase is complete.

**⚠️ CRITICAL**: Tasks T004–T011 must all complete before Phase 3 can start.

- [X] T004 Fix the pre-existing warmup timestamp bug (FR-032) in `cmd/wad/main.go`: source `SessionCreated` from `sessionStore.Load(ctx).CreatedAt()` instead of `time.Now()`. When the session is zero (not yet paired), default to `time.Now()` and update the dispatcher once pairing completes. This gates meaningful testing of per-profile warmup state.
- [X] T005 [P] Write `cmd/wad/profile_test.go` with (a) table-driven regex tests for FR-002, (b) exhaustive reserved-name rejection tests for FR-003 including verb and unit-type suffix collisions, (c) property test asserting `for all s matching regex: filepath.IsLocal(s) && strings.ToLower(s) == s && !strings.Contains(s, "--")` (SC-015), and (d) benchmark sub-test asserting `ValidateProfileName` completes in `<1ms` (SC-005).
- [X] T006 [P] Create `cmd/wad/runtime_dir.go` with `verifyRuntimeParent(path string) error` that performs the four pre-bind checks from `contracts/profile-paths.md §Runtime directory verification`: `Lstat` confirms directory, mode is `0700` exactly, `Uid == Geteuid()`, not a symlink (FR-042). Called from the socket adapter before `net.Listen`.
- [X] T007 [P] Create `cmd/wad/filelock_safe.go` wrapping `rogpeppe/go-internal/lockedfile.OpenFile` to pass `O_NOFOLLOW` on the underlying `os.OpenFile` call (FR-044, CVE-2025-68146). Includes a unit test that plants a symlink at the lock path and asserts the open returns `ELOOP`.
- [X] T008 Enhance the existing feature-004 socket adapter. **Inventory**: `peercred_{linux,darwin}.go` already implements `peerUID()`; `accept.go` already compares `uid != euid` and rejects (FR-045 ✓); `lock.go` already does lock-guarded stale-socket cleanup (FR-047 ✓); `listener.go` already checks parent-dir mode and symlink ownership (partial FR-042 ✓). **Gaps to fix**: (a) `listener.go` — tighten parent-dir check to require `mode == 0700` exactly (currently only checks not group/world writable); wrap `net.Listen` with `syscall.Umask(0o177)` / defer restore to close the bind→chmod TOCTOU (FR-043). (b) `lock.go` — add `syscall.O_NOFOLLOW` to the `os.OpenFile` call on the lockfile (FR-044, CVE-2025-68146). (c) `accept.go` — after the peercred check succeeds, write one `accept` audit entry via the `AuditLog` port with peer UID and PID, coalesced when the same PID repeats ≥10 times/sec (FR-046).
- [X] T009 [P] Add depguard/grep lint rule in `.golangci.yml` forbidding any `filepath.Join(..., profile, ...)` call outside `cmd/wad/profile.go` and `cmd/wa/profile.go` (FR-048 defense-in-depth). Also forbid `exec.Command(..., fmt.Sprintf(... profile ...))` patterns (FR-049 argv-only interpolation).
- [X] T010 [P] Add a grep-based lint or unit test asserting every join site inside the codebase that uses a profile name also calls `filepath.IsLocal` on the profile name immediately before the join (FR-048).
- [X] T011 Upgrade the profile data-dir open sites to use `os.Root`/`os.OpenRoot` (Go 1.24+) where feasible (FR-048). Confirm `go.mod` pins `go 1.25` or later (already current).

**Checkpoint**: All security primitives, the warmup bug fix, and the lint rules are in place. Phase 3+ can begin.

---

## Phase 3: User Story 1 — Migration (Priority: P1) 🎯 MVP

**Goal**: A 007-format install upgrades to 008 on first run with zero data loss, crash-safe under `SIGKILL`, reversible via `wa migrate --rollback`.

**Independent Test**: Start with a fixture `session.db` + `messages.db` + WAL/SHM sidecars + `allowlist.toml` + `audit.log` at 007 paths. Run the 008 `wad` binary with a `t.TempDir()` XDG root. Assert (a) all files moved into `default/` subdirectories, (b) WAL data preserved (no silent loss), (c) schema-version=2 and active-profile=default written atomically, (d) one audit entry with action `migrate`, (e) `wa migrate --rollback` restores the exact 007 layout.

### Tests for User Story 1

> Write these tests FIRST, ensure they FAIL before implementation.

- [X] T012 [P] [US1] Write `cmd/wad/migrate_test.go` with tests for (a) forward migration happy path with all 9 files including WAL/SHM sidecars, (b) idempotency — running migration twice is a no-op after schema-version=2, (c) rollback happy path, (d) rollback negative tests for each pre-condition violation, (e) pre-flight failures (EXDEV, free-space, ownership, DB-open), (f) WAL checkpoint correctness — seed a DB with pending WAL writes and assert data is preserved in destination main DB, (g) dry-run output matching a golden file. Uses `t.TempDir()` for isolation.
- [X] T013 [P] [US1] Write `cmd/wad/internal/migratefault/main.go` helper binary that executes the migration transaction and exposes a `WA_MIGRATE_KILL_AT=<step>` env-var hook for the crash-injection test.
- [X] T014 [US1] Write `cmd/wad/migrate_crash_test.go` (SC-013 + SC-001) that (a) launches the T013 helper via `os/exec`, sends `SIGKILL` between every pair of numbered steps in `contracts/migration.md §Canonical sequence` (steps 1..25), then restarts the process and asserts startup recovery restores the layout to either pre-migration-clean or post-migration-clean with zero data loss; and (b) runs a round-trip test (SC-001): seeds a fixture `session.db` + `messages.db` with known content at 007 paths, runs the 008 daemon to trigger migration, asserts the post-migration JID + allowlist + audit log entries match the pre-migration state byte-for-byte (minus the new `migrate` audit entry). Depends on T012 and T013.

### Implementation for User Story 1

- [X] T015 [US1] Create `cmd/wad/migrate.go` with the `MigrationTx` struct per data-model §4 (source fields for all 9 files incl. WAL/SHM sidecars, stagingDir, markerPath, dstResolver).
- [X] T016 [US1] Implement `MigrationTx.Plan() ([]MigrationStep, error)` with pre-flight checks: EXDEV comparison via `syscall.Stat_t.Dev`, free-space check (`2 × sum(sizes)`), ownership check (`Geteuid()`), no-DB-open check via `lsof`/procfs (contracts/migration.md §Pre-conditions).
- [X] T017 [US1] Implement WAL-checkpoint step: open `session.db` and `messages.db` via `modernc.org/sqlite`, issue `PRAGMA wal_checkpoint(TRUNCATE)` on each, close (contracts/migration.md step 3–5).
- [X] T018 [US1] Implement write-ahead marker: `MigrationTx.writeMarker()` writes `$XDG_CONFIG_HOME/wa/.migrating` with the move plan + timestamp via tempfile→fsync→rename, then fsyncs the parent (contracts/migration.md step 6–7).
- [X] T019 [US1] Implement staging copies: for each source file, `MigrationTx.stage()` copies to `default.new/` with explicit `fsync` on the destination fd before close; then fsyncs each `default.new/` directory (contracts/migration.md step 8–15).
- [X] T020 [US1] Implement the single pivot: `MigrationTx.pivot()` with build-tagged helpers — `pivot_linux.go` uses `unix.Renameat2(..., RENAME_EXCHANGE)`; `pivot_darwin.go` uses the two-step `rename("default", "default.old"); rename("default.new", "default")` with marker update between steps (contracts/migration.md step 16). Must fsync the pivot parent afterwards with `F_FULLFSYNC` on darwin.
- [X] T021 [US1] Implement post-pivot writes in `MigrationTx.finalize()`: atomic tempfile-rename for `.schema-version=2` and `active-profile=default`, each with fsync on file and parent (FR-018, contracts/migration.md step 17–20).
- [X] T022 [US1] Implement audit log entry and source unlinks in `MigrationTx.cleanup()`: append `migrate`/`wad:migrate`/`ok` entry to destination audit log, unlink source files, unlink `.migrating` marker (FR-019, contracts/migration.md step 21–24).
- [X] T023 [US1] Implement `MigrationTx.Recover()` startup recovery protocol that reads the `.migrating` marker and either completes forward (pivot succeeded) or rolls back (pre-pivot). Idempotent on re-run (contracts/migration.md §Recovery).
- [X] T024 [US1] Implement `MigrationTx.ApplyRollback()` with the six pre-conditions check (contracts/migration.md §Rollback), a `.migrating-rollback` marker, and mirror-sequence reverse moves that recreate WAL/SHM sidecars as empty stubs.
- [X] T025 [US1] Wire migration into `cmd/wad/main.go` startup: before adapter construction, if `.migrating` marker exists call `Recover()`; else if schema-version ≠ 2 and 007 layout detected, call `Apply()`. `Apply()` acquires **both** the advisory 007 `session.db.lock` flock **and** performs the `fuser`/procfs no-DB-open pre-flight check per FR-017 (flock alone is insufficient because `modernc.org/sqlite` does not share it).
- [X] T026 [US1] Create `cmd/wa/cmd_migrate.go` with the `wa migrate` Cobra subcommand accepting `--dry-run` and `--rollback` flags, mapping to `MigrationTx.Plan()` + print / `ApplyRollback()` respectively. Exit codes per `contracts/migration.md §Error handling`.

**Checkpoint**: Migration is fully functional, crash-safe, and reversible. Feature is MVP-deployable at this point for users who only need the silent-upgrade path.

---

## Phase 4: User Story 2 — Second profile pairing (Priority: P1)

**Goal**: A user with a paired `default` profile can pair a second `work` profile via `wa --profile work pair`, and both daemons run concurrently with full state isolation.

**Independent Test**: Start two `wad` processes (one per profile) in `t.TempDir()` isolation with memory-fake whatsmeow clients. Assert each uses its own socket, lock, session store, rate limiter, audit log. Send a message from each and confirm tokens consume from separate rate limiters.

### Tests for User Story 2

- [X] T027 [P] [US2] Write `cmd/wad/integration_test.go` `TestTwoProfileE2E` (SC-011 + SC-003): spawn two daemons with distinct profiles using memory-fake adapters, pair both, send **1000 sequential messages** from each profile, assert rate-limiter state is independent per profile (SC-003 no cross-contamination) and that isolation of allowlist + audit log + session state holds throughout. Must complete in <10s wall-clock (SC-011).
- [X] T028 [P] [US2] Write `internal/adapters/primary/socket/path_test.go` with unit tests for `PathFor(profile)` on both linux/darwin build tags, covering the `sun_path` budget hard-fail edge case.

### Implementation for User Story 2

- [X] T029 [US2] Modify `cmd/wad/main.go` to accept `--profile` Cobra flag, thread the resolved profile through `PathResolver`, call `ensureDirs(profile)`, `verifyRuntimeParent`, and pass profile-specific paths to every adapter constructor. Each profile's daemon process holds its own `whatsmeow.Adapter`, `app.Dispatcher`, `socket.Server`, `slogaudit.Audit`, `sqlitestore`, `sqlitehistory` (FR-030, FR-031).
- [X] T030 [P] [US2] **Enabled via T001 PathResolver.PairHTMLPath(); awaits PR #8** — `PathResolver.PairHTMLPath()` already returns `os.TempDir()/wa-pair-<profile>.html` per FR-014, and `TestPathResolver_Methods` asserts the `wa-pair-work.html` format. When `hotfix/browser-pair-qr` PR #8 eventually lands and introduces browser pair HTML code, the implementer calls `resolver.PairHTMLPath()` to get FR-014 compliance automatically. No additional work in feature 008 is possible because the browser-pair-HTML code does not yet exist in the codebase (verified via grep on 2026-04-11).
- [X] T031 [US2] Modify `cmd/wa/root.go` to add `--profile` as a persistent Cobra flag; call `ResolveProfile` (T032) at pre-run to populate the active profile for all subcommands.
- [X] T032 [US2] Create `cmd/wa/profile.go` with `ResolveProfile(flagValue string) (ResolvedProfile, error)` per data-model §3 implementing the four-step precedence chain (flag → env → file → default → singleton), with empty-string-treated-as-unset at every source (FR-001). Also implements multi-profile ambiguity error at exit 78 (FR-039) and the active-profile-file whitespace/BOM trim.
- [X] T033 [US2] Modify the dispatcher actor convention so each profile's audit entries set `Actor = "wad:" + profile` (FR-033). This is a string-formatting change in the composition root, not a domain change.

**Checkpoint**: Two profiles can run side-by-side with full process isolation. Together with US1, this is the core value of the feature.

---

## Phase 5: User Story 3 — Shell completion (Priority: P2)

**Goal**: `wa --profile <TAB>` and `wa profile use <TAB>` complete with the user's actual profile names in bash/zsh/fish/powershell.

**Independent Test**: `wa completion bash > /tmp/wa.bash && source /tmp/wa.bash`, then assert `complete -p wa` shows the custom function, and simulate tab completion returning the expected name set.

- [X] T034 [P] [US3] Create `cmd/wa/completion.go` with a `completeProfileNames` function that reads `filepath.Glob($XDG_DATA_HOME/wa/*/session.db)` and returns `(names, cobra.ShellCompDirectiveNoFileComp)`. Wire it via `rootCmd.RegisterFlagCompletionFunc("profile", completeProfileNames)` and also set `ValidArgsFunction` on the `profile use` and `profile rm` subcommands. Must complete in <50ms at 50 profiles (SC-008).
- [X] T035 [P] [US3] Write `cmd/wa/completion_test.go` with table-driven tests covering 0/1/3/20/50 profile counts, a prefix-match case (`w` → `work`), and an exclude-incomplete-profile case (mkdir'd but no session.db).

---

## Phase 6: User Story 4 — Active profile pointer (Priority: P2)

**Goal**: `wa profile use <name>` writes the active-profile pointer atomically; subsequent `wa` invocations without `--profile` target that profile.

**Independent Test**: Set active profile to `work`, run `wa status` without flags, assert it queries the work daemon. Switch back, assert it queries default.

- [X] T036 [P] [US4] Implement `wa profile use <name>` in `cmd/wa/cmd_profile.go`: validate name via `ValidateProfileName`, assert profile exists on disk, write `$XDG_CONFIG_HOME/wa/active-profile` atomically (tempfile → fsync → rename → fsync parent) with content `<name>\n` (FR-026, FR-018 idiom).
- [X] T037 [P] [US4] Implement `ListProfiles()` in `cmd/wa/profile.go` per data-model §5: glob `$XDG_DATA_HOME/wa/*/session.db`, read `active-profile`, probe each profile's socket for `connected`/`reconnecting`/`daemon-stopped`/`not-paired` status with a 200 ms RPC timeout, render JID or `(unknown)` on timeout (FR-025).

---

## Phase 7: User Story 5 — Service installation (Priority: P2)

**Goal**: Each profile can be installed as a system service (systemd user unit on Linux, launchd LaunchAgent on darwin) with per-profile state isolation, proper hardening directives, and no root.

**Independent Test**: On Linux, install two profiles as services, run `systemctl --user list-units 'wad@*'`, assert both are listed. On darwin, install two profiles, run `launchctl list | grep com.yolo-labz.wad`, assert both are listed.

### Tests for User Story 5

- [X] T038 [P] [US5] Update `cmd/wad/service_test.go` with (a) systemd template rendering golden test asserting all FR-034 hardening directives are present and MDWE is absent, (b) launchd plist rendering golden test asserting `KeepAlive` is a dict with `Crashed=true`/`SuccessfulExit=false`, `ProcessType=Background`, `EnvironmentVariables.PATH` set, `LimitLoadToSessionType` absent (FR-035), (c) injection-resistance test attempting to render with profile names containing XML metacharacters (rejected pre-template by `ValidateProfileName`).

### Implementation for User Story 5

- [X] T039 [US5] Modify `cmd/wad/service_linux.go` to render the systemd template unit at `~/.config/systemd/user/wad@.service` exactly as defined in `contracts/service-templates.md` (hardening directive set + NOTE ON SANDBOXING comment block, MDWE absent). Template is written once; subsequent profiles only `systemctl --user enable --now wad@<profile>.service`. Print the `loginctl enable-linger $USER` exact command once on first install (FR-037).
- [X] T040 [US5] Modify `cmd/wad/service_darwin.go` to render a per-profile plist at `~/Library/LaunchAgents/com.yolo-labz.wad.<profile>.plist` exactly as defined in `contracts/service-templates.md` (KeepAlive dict, ProcessType Background, EnvironmentVariables.PATH, no LimitLoadToSessionType), loaded via `launchctl bootstrap gui/$(id -u) <plist>`.
- [X] T041 [US5] Modify `cmd/wad/service.go` to thread `--profile <name>` through the shared install/uninstall plumbing. `wad install-service --profile <name>` and `wad uninstall-service --profile <name>` affect only the specified profile (FR-036). Both refuse to run as root (FR-038, unchanged from feature 007).

---

## Phase 8: User Story 6 — Profile lifecycle (Priority: P2)

**Goal**: `wa profile list/create/rm/show` subcommands expose full profile lifecycle management.

**Independent Test**: Create a profile, list to verify, `rm --force` to remove, list to verify gone. Negative tests for hard constraints (remove active, remove only, remove running).

### Tests for User Story 6

- [X] T042 [P] [US6] Write `cmd/wa/cmd_profile_test.go` using `rogpeppe/go-internal/testscript` to cover: `list` output formatting + ANSI escaping of invalid names (FR-025), **single-profile `wa status` output grep-asserting the word "profile" NEVER appears** (SC-002), `create` case-insensitive collision check (FR-027), `rm` hard-constraint refusals (active / only / running — FR-028), `rm --yes`/`-y` skips confirmation, and `show` metadata display. Uses a fake `wad` binary for socket probing.

### Implementation for User Story 6

- [X] T043 [US6] Implement `wa profile list` in `cmd/wa/cmd_profile.go`: call `ListProfiles()` (T037), render the `PROFILE  ACTIVE  STATUS  JID  LAST_SEEN` table with ANSI/control-character stripping via a dedicated `sanitizeProfileName` helper, hex-escape profile dir names that fail regex validation with an `(invalid)` marker (FR-025).
- [X] T044 [US6] Implement `wa profile create <name>` in `cmd/wa/cmd_profile.go`: validate name, case-insensitive collision check against `os.ReadDir($XDG_DATA_HOME/wa/)` before mkdir (FR-027), mkdir the profile tree via `ensureDirs`, seed an empty allowlist, print the pair-device hint. Refuses on collision at exit 64 with pointer to the existing cased sibling.
- [X] T045 [US6] Implement `wa profile rm <name>` in `cmd/wa/cmd_profile.go` with the three hard constraints (not active, not only, not running) per FR-028. `--yes` (short `-y`) skips interactive confirmation but still enforces hard constraints. **Per constitution §III there is no `--force` flag** — `--yes` is a prompt-skip, not a safety-bypass. Running-daemon check attempts to acquire the profile's `.lock` file.
- [X] T046 [US6] Implement `wa profile show [<name>]` in `cmd/wa/cmd_profile.go`: defaults to active profile if no argument; displays PathResolver-resolved paths + status + JID + LAST_SEEN.

**Checkpoint**: All six user stories are independently functional.

---

## Phase 9: Polish & Cross-Cutting Concerns

- [X] T047 [P] Update `CLAUDE.md` §Filesystem layout — add per-profile segment to all paths in the table. Remove "Multi-profile support" from §"Deferrable past v0.1" and add a brief §Multi-profile section documenting the `--profile` flag, precedence chain, and pointer to `specs/008-multi-profile/`.
- [X] T048 [P] Write the benchmark harness for SC-014: `cmd/wad/bench_test.go` with `BenchmarkProfileList` (SC-004 <100ms at 20 profiles), `BenchmarkMigration` (SC-006 <200ms at 100MB), `BenchmarkCompletion` (SC-008 <50ms at 50 profiles). Commit the baseline numbers to `specs/008-multi-profile/benchmarks.txt`.
- [X] T049 [P] Write the migration release note for CLAUDE.md §Release notes (or a new `docs/release-notes/008-multi-profile.md`) per research.md D7 §Release-notes requirement: "*wa transparently migrates your single-profile installation to a `default` profile on first run. No action is required. If anything goes wrong, `wa migrate --rollback` restores the prior layout.*"
- [X] T050 Run `go test -race ./...` and confirm zero race warnings across the entire codebase (SC-009). Fix any findings before merging.
- [X] T051 Run `golangci-lint run ./...` and confirm zero findings, including the `core-no-whatsmeow` and `app-no-adapters` depguard rules plus the new `filepath.Join` bypass lint from T009 (SC-010).
- [X] T052 Run the `quickstart.md` 10-step walkthrough end-to-end on a Linux + macOS test host, with the automated verification (`TestTwoProfileE2E` from T027) as the fallback. Record any step-count or output drift in `quickstart.md` so reviewers see intent.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies. T001 blocks T002/T003.
- **Phase 2 (Foundational)**: Depends on Phase 1. T004–T011 all block Phase 3+. Within Phase 2, T004–T011 can run in parallel except T008 (sequential with T006/T007).
- **Phase 3 (US1)**: Depends on Phase 2. Internal ordering: T012–T014 (tests) before T015–T026 (implementation); within implementation, T015 blocks T016–T024; T025/T026 are last (wire-up).
- **Phase 4 (US2)**: Depends on Phase 2. Can run in parallel with Phase 3 if staffed.
- **Phase 5 (US3)**: Depends on Phase 4 (needs `PathResolver` + `ListProfiles` wiring).
- **Phase 6 (US4)**: Depends on Phase 4.
- **Phase 7 (US5)**: Depends on Phase 4.
- **Phase 8 (US6)**: Depends on Phase 4 + Phase 6 (needs `ListProfiles` from T037).
- **Phase 9 (Polish)**: Depends on all prior phases complete.

### User Story Dependencies

- **US1 (P1 migration)**: Independent after Phase 2. Ships alone as a silent-upgrade MVP.
- **US2 (P1 second profile)**: Independent after Phase 2. Can parallelise with US1.
- **US3 (P2 completion)**: Depends on US2's `ResolveProfile`.
- **US4 (P2 active profile)**: Depends on US2's `ResolveProfile`.
- **US5 (P2 services)**: Depends on US2's flag threading; independent of US3/US4/US6.
- **US6 (P2 lifecycle)**: Depends on US4's `ListProfiles`.

### Parallel Opportunities

- Phase 1: T001 first, then T002 + T003 in parallel.
- Phase 2: T005 + T006 + T007 + T009 + T010 all in parallel after T001. T004 is independent. T008 sequentialises after T006/T007. T011 after T001.
- Phase 3: T012 + T013 in parallel (tests for different concerns). T015 blocks T016–T024 (shared `migrate.go` file). T026 (`cmd_migrate.go`) is independent.
- Phase 4: T027 + T028 in parallel. T029 sequentialises with T030 only if same `main.go` (they're different files — can parallel). T031 + T032 + T033 can parallel after T029.
- Phase 5+6+7+8 can run in parallel once Phase 4 completes.
- Phase 9: T047 + T048 + T049 in parallel. T050/T051 sequential after all implementation. T052 last.

---

## Parallel Example: User Story 1

```bash
# Launch all US1 tests together (different files):
Task: "Write cmd/wad/migrate_test.go unit+golden+idempotency tests (T012)"
Task: "Write cmd/wad/internal/migratefault/main.go fault helper (T013)"

# Launch migration implementation subtasks in file order:
Task: "Create cmd/wad/migrate.go MigrationTx struct (T015)"  # sequential — shared file
Task: "Implement Plan() pre-flight (T016)"                    # sequential
Task: "Implement WAL checkpoint (T017)"                       # sequential
Task: "Implement marker write (T018)"                         # sequential
Task: "Implement staging (T019)"                              # sequential
Task: "Implement pivot with build-tagged helpers (T020)"      # sequential, but separate files OK
Task: "Implement finalize (T021)"                             # sequential
Task: "Implement cleanup (T022)"                              # sequential
Task: "Implement Recover (T023)"                              # sequential
Task: "Implement ApplyRollback (T024)"                        # sequential

# Final wire-up runs after T015–T024:
Task: "Wire migration into cmd/wad/main.go (T025)"
Task: "Create cmd/wa/cmd_migrate.go subcommand (T026)"
```

---

## Implementation Strategy

### MVP First (US1 Migration alone)

1. Complete Phase 1: Setup (T001–T003).
2. Complete Phase 2: Foundational (T004–T011).
3. Complete Phase 3: US1 Migration (T012–T026).
4. **STOP and VALIDATE**: existing 007 users upgrade to 008 with the silent migration. No new subcommands exposed yet.
5. Ship as a point release. Users who only care about the warmup bug fix and the crash-safe migration benefit immediately.

### Incremental Delivery

1. MVP above → US2 second profile (core value) → ship (008.0).
2. US3 (completion) + US4 (active profile pointer) → ship (008.1).
3. US5 (services) + US6 (lifecycle) → ship (008.2).
4. Polish → release notes → ship final.

### Parallel Team Strategy

- Dev A owns US1 end-to-end (migration is the largest block).
- Dev B owns US2 + US4 (profile flag threading + active-profile).
- Dev C owns US3 + US5 + US6 (completion + services + lifecycle).
- All three sync on Phase 2 foundational completion before branching.

---

## Notes

- **Constitution rule 6** (cap `tasks.md` at ~25 items) is intentionally exceeded. See the "Scope note" at the top of this file for justification. If a reviewer disagrees, the recommended split is: **008a** = Setup + Foundational + US1 (migration); **008b** = US2 + US3 + US4 + US5 + US6 + Polish. The split adds a PR boundary but does not reduce total task count.
- **Tests are NOT optional** for this feature. Every user story has at least one test task. The crash-injection test (T014) is mandated by SC-013 and is non-negotiable — the migration is a data-loss risk without it.
- Zero changes to `internal/domain/` or `internal/app/` or `internal/app/ports.go` (SC-012). `core-no-whatsmeow` and `app-no-adapters` depguard rules continue to enforce this at CI time.
- Every task lists an exact file path. LLM implementers can read the file path and the FR reference without needing additional context.
- All paths use the `PathResolver` from T001 — no ad-hoc `filepath.Join(xdg.DataHome, "wa", ...)` calls are permitted outside the resolver (contracts/profile-paths.md §Path resolution code contract, enforced by T009 lint rule).
- **Hotfix dependency**: `hotfix/browser-pair-qr` (PR #8) must merge before T030 to avoid a conflict on `pair_html.go`.
- Commit boundaries: one commit per task, conventional-commit prefix `feat(adapter)`, `feat(app)`, `fix(adapter)`, `test(adapter)`, `docs(spec)` as appropriate. See `cliff.toml`.
