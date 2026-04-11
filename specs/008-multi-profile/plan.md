# Implementation Plan: Multi-Profile Support

**Branch**: `008-multi-profile` | **Date**: 2026-04-11 | **Spec**: [spec.md](spec.md)
**Research**: [research.md](research.md) (Swarm 2, D1-D8) | **Refactor inventory**: [refactor.md](refactor.md) (Swarm 1)

## Summary

Add per-profile isolation to the `wa` daemon and CLI. Each profile runs as its own `wad` process with its own `session.db`, `messages.db`, `allowlist.toml`, `audit.log`, and unix socket. Backward compatible: existing single-profile installs are silently migrated to a `default` profile on first run of the 008 binary. Zero changes to `internal/domain/`, `internal/app/`, or any adapter — the entire refactor lives in the composition root (`cmd/wad`), the CLI root (`cmd/wa`), and the socket path resolver (`internal/adapters/primary/socket/path_*.go`).

## Technical Context

**Language/Version**: Go 1.25 (unchanged)
**New dependencies**: None. Uses stdlib only + existing `github.com/adrg/xdg`, `github.com/spf13/cobra`, `github.com/rogpeppe/go-internal/lockedfile`.
**Storage**: Per-profile directories under XDG base paths. Schema version file at `$XDG_CONFIG_HOME/wa/.schema-version`.
**Testing**: Existing test patterns (memory fakes, `t.TempDir()`, goleak). No new test infrastructure. New tests for profile name validation, path resolution, migration transaction, completion.
**Target Platform**: Linux + macOS. Reserved names future-proof a potential Windows port.
**Constraints**: `CGO_ENABLED=0`; no whatsmeow imports outside adapters; `app-no-adapters` depguard rule preserved; zero port interface changes (SC-012).
**Performance**: Profile name validation <1ms; `wa profile list` <100ms at 20 profiles; migration <200ms for 100MB state; shell completion <50ms at 50 profiles.

## Constitution Check

| # | Principle | Status | Notes |
|---|---|---|---|
| I | Hexagonal core | ✅ PASS | Zero changes to `internal/domain/` or `internal/app/` or `internal/app/ports.go`. All profile plumbing lives in `cmd/wad/`, `cmd/wa/`, and `internal/adapters/primary/socket/path_*.go`. |
| II | Daemon owns state | ✅ PASS | Strengthened — each profile's daemon exclusively owns that profile's state. |
| III | Safety first | ✅ PASS | Each profile has its own full safety pipeline. Also fixes the pre-existing warmup timestamp bug (FR-032) as a prerequisite inside this feature. |
| IV | CGO forbidden | ✅ PASS | No new deps. |
| V | Spec-driven with citations | ✅ PASS | research.md has D1-D8 with primary-source URLs from Swarm 2. refactor.md has file:line references from Swarm 1. |
| VI | Port-boundary fakes | ✅ PASS | Existing memory adapter already supports multiple instances. No fake changes needed. |
| VII | Conventional commits | ✅ PASS | Inherited. |

**Gate result**: PASS. Zero violations. Phase 1 may proceed.

## Project Structure

### Documentation

```text
specs/008-multi-profile/
├── plan.md                  # This file
├── spec.md                  # 41 FRs, 12 SCs, 6 user stories
├── research.md              # D1-D8 from Swarm 2 (prior art, citations)
├── refactor.md              # Swarm 1 codebase inventory
├── data-model.md            # Profile entity, migration transaction, path resolver
├── quickstart.md            # 10-step two-profile reproducible bring-up
├── contracts/
│   ├── profile-paths.md     # Canonical path layout per profile
│   ├── migration.md         # Migration transaction protocol
│   └── service-templates.md # systemd template + launchd plist specs
└── checklists/
    └── requirements.md      # 49/49 pass
```

### Source Code Changes

**New files** (production):

```text
cmd/wad/profile.go            # Profile name validation (regex + reserved list), PathResolver
cmd/wad/dirs.go               # ensureDirs(profile) — create per-profile state tree with 0700
cmd/wad/runtime_dir.go        # verifyRuntimeParent — FR-042 Lstat+mode+owner+non-symlink check
cmd/wad/filelock_safe.go      # Wrap rogpeppe/go-internal/lockedfile with O_NOFOLLOW (FR-044)
cmd/wad/migrate.go            # MigrationTx: staging + WAL checkpoint + pivot + fsync
cmd/wad/pivot_linux.go        # build-tagged: renameat2(RENAME_EXCHANGE)
cmd/wad/pivot_darwin.go       # build-tagged: two-step rename + F_FULLFSYNC
cmd/wad/internal/migratefault/main.go  # Helper binary for crash-injection test (T013)

cmd/wa/profile.go             # ResolveProfile precedence chain + ListProfiles enumeration
cmd/wa/cmd_profile.go         # `wa profile [list|use|create|rm|show]` subcommand tree
cmd/wa/cmd_migrate.go         # `wa migrate [--dry-run|--rollback]`
cmd/wa/completion.go          # Cobra RegisterFlagCompletionFunc for profile names

internal/adapters/primary/socket/
├── path_linux.go             # MODIFIED — add PathFor(profile) sibling
├── path_darwin.go            # MODIFIED — add PathFor(profile) sibling
└── server.go                 # MODIFIED — umask-guarded bind, peercred check, accept audit
```

**Modified files**:

```text
cmd/wad/main.go              # ~60 lines: --profile flag, thread through paths,
                             # call migrate before adapter open, source SessionCreated
                             # from session store (FR-032 bug fix)
cmd/wad/dirs.go              # ~20 lines: ensureDirs(profile string)
cmd/wad/service.go           # ~40 lines: thread profile through install/uninstall
cmd/wad/service_darwin.go    # ~30 lines: profile-aware Label + plist path
cmd/wad/service_linux.go     # ~40 lines: systemd template unit
cmd/wa/root.go               # ~20 lines: add --profile persistent flag, resolution chain
internal/adapters/secondary/whatsmeow/pair_html.go  # ~5 lines: profile-suffixed temp file
```

**New tests**:

```text
cmd/wad/profile_test.go      # Regex validation + reserved name table tests
cmd/wad/migrate_test.go      # Migration forward, idempotent, rollback, interrupted
cmd/wad/service_test.go      # MODIFIED — add profile-aware template assertions
cmd/wa/cmd_profile_test.go   # Subcommand test via testscript
internal/adapters/primary/socket/path_test.go  # PathFor(profile) unit tests
```

**Documentation updates**:

```text
CLAUDE.md                    # §Filesystem layout — add profile segment
                             # §Multi-profile — remove "deferred past v0.1" marker
```

**Structure Decision**: Per feature 001, adapters live under `internal/adapters/`, binaries under `cmd/`. This feature touches only the composition root (`cmd/wad`), CLI (`cmd/wa`), and the socket path resolver. The hexagonal core is untouched. This is the smallest-surface-area refactor that adds multi-tenancy to a hexagonal app.

## Complexity Tracking

No constitution violations.

| Apparent complexity | Why it's necessary | Simpler alternative rejected because |
|---|---|---|
| Two-swarm research workflow | User requested it; feature has 5 orthogonal decisions each needing prior art | Single research pass — would have been shallower per dimension |
| Bundled warmup bug fix | The bug (time.Now() instead of session timestamp) manifests per-profile testing | Separate PR — would force merge coordination and delay 008 |
| Migration transaction inside existing flock | Atomicity requirement (FR-016); no mid-state possible | New global lock — would deadlock with existing sqlstore flock |
| systemd template unit (one file, N symlinks) | Idiomatic pattern per research D2 | Per-file — clutters `~/.config/systemd/user/` and requires bookkeeping |

## Estimated LoC Budget (revised 2026-04-11 post-analysis)

| Category | LoC |
|---|---|
| `cmd/wad/profile.go` (regex + reserved list + PathResolver) | 140 |
| `cmd/wad/dirs.go` (ensureDirs) | 35 |
| `cmd/wad/runtime_dir.go` (FR-042 parent verification) | 45 |
| `cmd/wad/filelock_safe.go` (FR-044 O_NOFOLLOW wrapper) | 40 |
| `cmd/wad/migrate.go` (MigrationTx + Plan + Apply + Recover + Rollback + WAL checkpoint) | 360 |
| `cmd/wad/pivot_linux.go` (renameat2 RENAME_EXCHANGE) | 35 |
| `cmd/wad/pivot_darwin.go` (two-step rename + F_FULLFSYNC) | 55 |
| `cmd/wad/internal/migratefault/main.go` (fault-injection helper binary) | 60 |
| `cmd/wad/main.go` modifications (warmup bug fix + --profile threading + migration wire-up) | 90 |
| `cmd/wad/service*.go` (hardened systemd template + launchd dict + profile threading) | 150 |
| `cmd/wa/profile.go` (ResolveProfile + ListProfiles) | 150 |
| `cmd/wa/cmd_profile.go` (list + use + create + rm + show) | 200 |
| `cmd/wa/cmd_migrate.go` | 50 |
| `cmd/wa/completion.go` | 50 |
| `cmd/wa/root.go` modifications (--profile persistent flag + pre-run resolver) | 40 |
| `internal/adapters/primary/socket/path_*.go` (PathFor + sun_path check) | 50 |
| `internal/adapters/primary/socket/server.go` modifications (umask+peercred+accept audit) | 100 |
| `internal/adapters/secondary/whatsmeow/pair_html.go` (profile suffix) | 10 |
| **Production subtotal** | **~1660** |
| `cmd/wad/profile_test.go` (validation + property test) | 120 |
| `cmd/wad/migrate_test.go` (forward/rollback/idempotent/WAL/golden) | 280 |
| `cmd/wad/migrate_crash_test.go` (subprocess SIGKILL injection, SC-013) | 150 |
| `cmd/wad/bench_test.go` (SC-014 benchmark harness) | 80 |
| `cmd/wad/runtime_dir_test.go` | 40 |
| `cmd/wad/filelock_safe_test.go` (symlink rejection) | 30 |
| `cmd/wad/integration_test.go` (two-profile e2e, SC-011) | 180 |
| `cmd/wad/service_test.go` additions (golden tests for template + plist) | 120 |
| `cmd/wa/cmd_profile_test.go` (testscript coverage) | 150 |
| `cmd/wa/completion_test.go` | 40 |
| `internal/adapters/primary/socket/path_test.go` | 40 |
| **Test subtotal** | **~1230** |
| **Total** | **~2890 LoC** |

Roughly **3.3×** the pre-analysis estimate of 865 LoC. The growth is dominated by (a) the crash-safe migration transaction (~360 LoC in migrate.go + 150 LoC in the fault-injection test + 90 LoC in build-tagged pivot helpers), (b) the socket-security FRs added in the post-checklist revision (FR-042..FR-049 → runtime_dir.go + filelock_safe.go + server.go modifications), and (c) the two-profile e2e integration test that exercises the full dispatcher stack. Still within the feature-size envelope of features 004 (socket adapter) and 005 (app usecases).

If the scope proves too large for a single PR, the recommended split is **008a** (Setup + Foundational + US1 Migration, ~1600 LoC) and **008b** (US2–US6 + Polish, ~1290 LoC).

## Phase 0: Research — ALREADY COMPLETE

Phase 0 was produced by two parallel agent swarms before this plan was written:

- **Swarm 1 (codebase exploration)**: 5 agents mapped paths, wiring, adapters, tests+packaging, and constitution/safety. Output: [refactor.md](refactor.md).
- **Swarm 2 (web research)**: 5 agents researched multi-profile patterns, service templates, XDG layout, name validation, and migration UX. Output: [research.md](research.md) with D1-D8 decision blocks and citations.

No `[NEEDS CLARIFICATION]` markers remain.

## Phase 1: Design & Contracts

Next artifacts in this command:

1. **data-model.md** — Profile entity, PathResolver struct, MigrationTx struct, ProfileList derivation
2. **contracts/profile-paths.md** — Canonical layout table + validation rules
3. **contracts/migration.md** — Transaction protocol, rollback conditions, atomicity guarantees
4. **contracts/service-templates.md** — systemd template unit + launchd plist templates
5. **quickstart.md** — Two-profile end-to-end walkthrough
6. **Agent context update** via `update-agent-context.sh`
