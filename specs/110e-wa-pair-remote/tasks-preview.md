# Feature 110e — Tasks preview

Preview of the upcoming `/speckit:tasks` output. ≤ 25 rows per Constitution §I.6. Current count: **14 tasks**. `[P]` = parallel-safe within the same phase.

## Phase 1 — Parser + flag (foundation)

| # | Task | Files touched | Test name | Spec FR |
|---|------|---------------|-----------|---------|
| T1-01 | Define `RemoteTarget` struct + error types in new file. | `cmd/wa/cmd_pair_remote.go` (new) | compile | FR-001 |
| T1-02 [P] | Implement `ParseRemoteTarget` with all 9 cases from `data-model.md` test matrix. | `cmd/wa/cmd_pair_remote.go` (mod) | `TestParseRemoteTarget_HappyPath`, `TestParseRemoteTarget_URLRejected`, `TestParseRemoteTarget_EmptyRejected`, `TestParseRemoteTarget_MissingSeparator`, `TestParseRemoteTarget_EmptyHost`, `TestParseRemoteTarget_EmptyApp`, `TestParseRemoteTarget_MultiColonApp`, `TestParseRemoteTarget_UserAtHost` (table-driven) | FR-002, FR-003 |
| T1-03 | Wire `pairRemote string` cobra flag in `cmd_pair.go` `init()`. Update long-description to include `--remote ProxMox.Dokku:wa-burocracy` example. | `cmd/wa/cmd_pair.go` (mod) | `TestPairHelpShowsRemoteFlag` (golden-file on `wa pair --help` output) | FR-001 |

**Exit gate**: parser unit tests green; `wa pair --help` shows the flag.

## Phase 2 — Exec helper

| # | Task | Files touched | Test name | Spec FR |
|---|------|---------------|-----------|---------|
| T2-01 | Define `var execCommand = exec.Command` indirection (test seam). | `cmd/wa/cmd_pair_remote.go` (mod) | compile | FR-001 |
| T2-02 | Implement `runPairRemote(target RemoteTarget, extraFlags []string) error`. Build argv `["ssh", "-t", host, "dokku", "enter", app, "--", "/usr/local/bin/wa", "pair", extraFlags...]`. Inherit stdio. | `cmd/wa/cmd_pair_remote.go` (mod) | `TestRunPairRemote_ArgvShape` (fake `execCommand` records argv) | FR-001, FR-005 |
| T2-03 | Wire `runPairRemote` into `cmd_pair.go` `RunE` short-circuit (above the existing `callAndClose` path). Collect existing flags (`--phone`, `--browser`, `--idempotency-key`) into `extraFlags`. | `cmd/wa/cmd_pair.go` (mod) | `TestPairRouting_RemoteWinsOverSocket` (asserts `runPairRemote` invoked, not `callAndClose`) | FR-001, FR-005 |
| T2-04 [P] | Implement `ssh` binary lookup with `exec.LookPath("ssh")`. Return exit-70 `remoteParseError` if absent. | `cmd/wa/cmd_pair_remote.go` (mod) | `TestRunPairRemote_SSHMissing` (uses `t.Setenv("PATH", tmpEmptyDir)`) | FR-006 |

**Exit gate**: argv-shape + ssh-missing tests green.

## Phase 3 — Tests

| # | Task | Files touched | Test name | Spec FR |
|---|------|---------------|-----------|---------|
| T3-01 [P] | Add table-driven argv-shape test covering 3 flag combos (bare, `--phone`, `--browser`, `--idempotency-key`, all combined). | `cmd/wa/cmd_pair_remote_test.go` (new) | `TestRunPairRemote_ArgvShape` (table-driven, 5 rows) | FR-001, FR-005 |
| T3-02 [P] | Add testscript fixture with stub `ssh` shell script in `$WORK/bin/`. Stub echoes argv; test asserts the printed line matches an expected fixture. | `testscript/cmd_pair_remote.txt` (new) | `TestScript` (rogpeppe/go-internal/testscript) | FR-001 |
| T3-03 [P] | Negative test: `--remote https://wa.example.com` exits 64 with the spec-mandated message substring. | `cmd/wa/cmd_pair_remote_test.go` (mod) | `TestPairRemote_URLFormRefused` | FR-003 |
| T3-04 | Verify daemon-side diff is empty (no edits to `internal/`). | none — review-time check | `git diff origin/main -- internal/` returns empty | FR-007 |

**Exit gate**: `go test -race -count=1 ./cmd/wa/... ./testscript/...` green.

## Phase 4 — Docs

| # | Task | Files touched | Test name | Spec FR |
|---|------|---------------|-----------|---------|
| T4-01 | Add "Re-pair from a remote workstation" section to `docs/deploy/dokku.md` referencing `wa pair --remote ProxMox.Dokku:wa-burocracy`. Include the 3 invocations and the 3 troubleshooting lines from `quickstart.md`. | `docs/deploy/dokku.md` (mod) | markdown-lint passes | SC-004 |
| T4-02 [P] | Add 110e to the cross-link list in `specs/110c-wa-remote-cli/spec.md` "Future work" section so reviewers find the extension. | `specs/110c-wa-remote-cli/spec.md` (mod) | manual review | meta |
| T4-03 [P] | Update `CHANGELOG.md` via `git-cliff` (auto-generated on release tag — no hand edit; task is a "verify auto-gen works for this PR" check). | none — verify-only | `git cliff --unreleased` includes the 110e commits | meta / Constitution §V.25 |

**Exit gate**: docs render correctly in GitHub Markdown preview; cross-link present; changelog auto-gen works.

## Forbidden patterns (build-breakers if seen in tasks output)

- Any `files touched` entry under `internal/` paths → reject.
- Any `go get` of a non-stdlib package (other than the existing `rogpeppe/go-internal/testscript`) → reject.
- Any task that edits `internal/adapters/secondary/whatsmeow/pair*.go` → reject (FR-007 enforcement).
- Any task that adds a JSON-RPC method or REST endpoint → reject (FR-007 + DR-003).

## Parallelisation

`[P]` markers indicate same-phase parallel safety. Across phases, dependencies are linear (P1 → P2 → P3 → P4).

Within P1: T1-02 can start before T1-03.
Within P2: T2-04 parallel with T2-02/T2-03.
Within P3: T3-01, T3-02, T3-03 all parallel.
Within P4: T4-02, T4-03 parallel with T4-01.

## Estimated work

- LOC delta: ~+250 (production) + ~+150 (tests) + ~+30 (docs).
- Wall-clock: 2-3 hours for a focused session.
- CI runtime impact: +30s (one new testscript fixture + extra unit tests in cmd/wa).

## Sign-off

Ready for `/speckit:tasks`. Tasks generator should consume this preview verbatim (with minor adjustments per Constitution §III.15 — `[x]` only after named test passes).
