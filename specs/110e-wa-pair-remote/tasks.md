# Tasks — 110e wa pair --remote

**Feature**: 110e-wa-pair-remote · **Branch**: 147-wa-pair-remote · **Date**: 2026-05-19 · **Cap**: ≤ 25 tasks per Constitution §I.6.

Covers FR-001..FR-008 (CLI flag, parser, exec helper, URL refusal, flag pass-through, ssh-missing, daemon-untouched invariant, backwards-compat). Every task closes on a named passing test OR a contract-test file committed. `[P]` = parallelisable within the same phase.

## Phase 1 — Parser + flag (foundation)

| # | Task | Files (new unless marked mod) | Tests / Gates | FR / R |
|---|---|---|---|---|
| T1-01 | Create `cmd_pair_remote.go` skeleton: package `main`, imports (`errors`, `fmt`, `os/exec`, `strings`), define `RemoteTarget` struct with `Host string`, `App string`, define `remoteParseError` type implementing `Error() string` + `ExitCode() int` returning `64`. | `cmd/wa/cmd_pair_remote.go` | compile (`go build ./cmd/wa/...` clean) | FR-001, FR-002 |
| T1-02 [P] | Implement `ParseRemoteTarget(s string) (RemoteTarget, error)` covering every row of `data-model.md` test matrix: empty input, missing `:`, empty host, empty app, valid forms, `user@host` form, multi-colon app name, `http://`/`https://` URL refusal with the spec-mandated FR-003 message. Error messages must contain the substring `"expected <host>:<app>"` or `"pair requires SSH access"` per row. | `cmd/wa/cmd_pair_remote.go` (mod) | `TestParseRemoteTarget` (table-driven covering 9 rows from data-model.md §Test coverage matrix) | FR-002, FR-003 |
| T1-03 | Register `pairRemote string` cobra flag (`--remote`) in `cmd/wa/cmd_pair.go` `init()`. Update `pairCmd.Short` + `pairCmd.Long` to mention the flag. `--remote` help text MUST include the example `--remote ProxMox.Dokku:wa-burocracy`. | `cmd/wa/cmd_pair.go` (mod) | `TestPairHelpShowsRemoteFlag` (golden-string check that `wa pair --help` output contains `--remote`, `<ssh-host>:<dokku-app>`, and the ProxMox.Dokku example) | FR-001 |

## Phase 2 — Exec helper (the SSH wrap)

| # | Task | Files (new unless marked mod) | Tests / Gates | FR / R |
|---|---|---|---|---|
| T2-01 | Add package-level test seam: `var execCommand = exec.Command` so unit tests can inject a fake. NEVER reference `os/exec.Command` directly outside `execCommand` in `cmd_pair_remote.go`. | `cmd/wa/cmd_pair_remote.go` (mod) | compile | FR-001 |
| T2-02 | Implement `runPairRemote(target RemoteTarget, extraFlags []string) error`. Build argv literally: `["ssh", "-t", target.Host, "dokku", "enter", target.App, "--", "/usr/local/bin/wa", "pair"]` then append `extraFlags`. Set `cmd.Stdin = os.Stdin`, `cmd.Stdout = os.Stdout`, `cmd.Stderr = os.Stderr`. Return the `*exec.ExitError` exit code untranslated (operator sees SSH/dokku-enter's own exit). | `cmd/wa/cmd_pair_remote.go` (mod) | `TestRunPairRemote_ArgvShape` (5-row table: bare, --phone, --browser, --idempotency-key, all combined) | FR-001, FR-005 |
| T2-03 | Wire `runPairRemote` short-circuit into `cmd/wa/cmd_pair.go` `RunE`: if `pairRemote != ""`, call `ParseRemoteTarget(pairRemote)`, collect `pairPhone`, `pairBrowser`, `pairIdempotencyKey` flags into `extraFlags`, call `runPairRemote`, return. The existing socket path (lines ~92–110 of cmd_pair.go) is untouched and remains the default branch. | `cmd/wa/cmd_pair.go` (mod) | `TestPairRouting_RemoteWinsOverSocket` (injects fake `execCommand` that records argv; asserts socket-side `callAndClose` NOT invoked when `--remote` set) | FR-001, FR-005, FR-008 |
| T2-04 [P] | Pre-flight ssh-binary check inside `runPairRemote`. If `exec.LookPath("ssh")` returns an error, short-circuit with `remoteParseError{Code: 70, Message: "ssh binary not found in PATH; install OpenSSH client or fix PATH"}`. Do NOT call `execCommand`. | `cmd/wa/cmd_pair_remote.go` (mod) | `TestRunPairRemote_SSHMissing` (sets `t.Setenv("PATH", t.TempDir())`, expects exit 70 + stderr substring `"ssh binary not found"`) | FR-006 |

## Phase 3 — Tests

| # | Task | Files (new unless marked mod) | Tests / Gates | FR / R |
|---|---|---|---|---|
| T3-01 [P] | Write `cmd_pair_remote_test.go` with the `ParseRemoteTarget` table from T1-02, the argv-shape table from T2-02, the URL-refusal case, and the ssh-missing case. Use `testing.T.Setenv` for PATH-mutation; reset `execCommand` to its production value in `t.Cleanup`. | `cmd/wa/cmd_pair_remote_test.go` | `TestParseRemoteTarget`, `TestRunPairRemote_ArgvShape`, `TestPairRemote_URLFormRefused`, `TestRunPairRemote_SSHMissing` — all green under `go test -race -count=1 ./cmd/wa/...` | FR-001, FR-002, FR-003, FR-005, FR-006 |
| T3-02 [P] | Create `testscript/cmd_pair_remote.txt`. The fixture writes a stub `ssh` shell script into `$WORK/bin/` that does `printf '%s\n' "$0" "$@"` then `exit 0`. Test sets `PATH=$WORK/bin:$PATH`. Then invokes `wa pair --remote ProxMox.Dokku:wa-burocracy` and asserts stdout contains the exact argv expected (single line `ssh -t ProxMox.Dokku dokku enter wa-burocracy -- /usr/local/bin/wa pair` per `printf` semantics — adjust to multi-line if `printf` outputs one arg per line). | `testscript/cmd_pair_remote.txt` | `TestScript` (existing `testscript/main_test.go` runner picks up the new `.txt`) | FR-001 |
| T3-03 [P] | Negative test variant within `cmd_pair_remote_test.go`: `--remote https://wa.example.com` exits 64 with stderr substring `"pair requires SSH access to the daemon's host"`. Same for `http://`. | `cmd/wa/cmd_pair_remote_test.go` (mod from T3-01) | `TestPairRemote_URLFormRefused` (table-driven 2 rows: `https://`, `http://`) | FR-003 |
| T3-04 | Verification gate: assert daemon-side diff is empty. Add a one-line `Makefile`-style command or just document in `tasks.md` Done criteria: `git diff origin/main -- internal/ docs/ | wc -l` returns `0` for internal/, non-zero only for docs/. (No test code; review gate.) | none — verification only | `git diff origin/main -- internal/` returns EMPTY | FR-007 |

## Phase 4 — Docs

| # | Task | Files (new unless marked mod) | Tests / Gates | FR / R |
|---|---|---|---|---|
| T4-01 | Append new section "Re-pair from a remote workstation" to `docs/deploy/dokku.md` (after the existing "Pairing" section). Include the 3 invocations from `quickstart.md` (QR, --browser, --phone) and the 3 troubleshooting bullets (SSH key not loaded, dokku app missing, already paired). | `docs/deploy/dokku.md` (mod) | manual review + `markdownlint docs/deploy/dokku.md` passes if lint config exists | SC-004 |
| T4-02 [P] | Cross-link in `specs/110c-wa-remote-cli/spec.md` "Future work" or similar section: "110e extends this with `wa pair --remote <host>:<app>` for SSH-keyed pair." (1 line near the end). | `specs/110c-wa-remote-cli/spec.md` (mod) | manual review (reviewer can `grep -n 110e specs/110c-wa-remote-cli/spec.md` and see the line) | meta |
| T4-03 [P] | Cobra long-description polish on `pairCmd` in `cmd/wa/cmd_pair.go`: add a worked example block showing both local and `--remote` invocations. Verifies through the existing `wa pair --help` golden assertion in T1-03 (no separate test). | `cmd/wa/cmd_pair.go` (mod) | covered by `TestPairHelpShowsRemoteFlag` (T1-03 already asserts substring) | SC-003 |

## Done criteria

- Every `[ ]` becomes `[x]` only after the named test in column "Tests / Gates" passes (Constitution §III.15). No `[x]` without evidence.
- `go test -race -shuffle=on -count=1 ./cmd/wa/... ./testscript/...` green.
- `golangci-lint run --timeout 5m ./...` green (no new violations introduced; pre-existing T079-deferred gocyclo nolint in `cmd/wad/main.go` remains valid).
- `git diff origin/main -- internal/` returns EMPTY (FR-007 enforcement gate; daemon-side untouched).
- `git diff origin/main -- 'cmd/wa/*' 'testscript/cmd_pair_remote.txt' 'docs/deploy/dokku.md' 'specs/110c-wa-remote-cli/spec.md'` is non-empty and matches the touch list above.
- Operator quickstart works end-to-end against `ProxMox.Dokku:wa-burocracy` (manual integration check; NOT CI-gated per spec §Assumptions #5).

## Forbidden patterns (row-level rejects)

- Any "Files touched" entry under `internal/` paths — REJECTED.
- Any task adding a non-stdlib Go dependency beyond the already-present `rogpeppe/go-internal/testscript` (in `go.sum`) — REJECTED.
- Any task editing `internal/adapters/secondary/whatsmeow/pair*.go` — REJECTED (FR-007 strict).
- Any task adding a JSON-RPC method or REST endpoint — REJECTED (DR-003 + spec §"Out of scope").
- Any task implementing `wa panic --remote` or `wa session logout-all --remote` — REJECTED (spec §"Out of scope"; follow-up specs 110f / 110g).

## Parallel groups

| Phase | Group | Tasks | Notes |
|---|---|---|---|
| 1 | A | T1-02 | Parser can be implemented in parallel with T1-03 flag wiring once T1-01 skeleton lands. |
| 2 | B | T2-04 | ssh-missing check independent of T2-02 argv build + T2-03 routing. |
| 3 | C | T3-01, T3-02, T3-03 | All three test additions touch disjoint files (`cmd_pair_remote_test.go` and `testscript/cmd_pair_remote.txt`); rows tagged `[P]` can land in any order. |
| 4 | D | T4-02, T4-03 | Cross-link edit on 110c spec + cobra long-description polish are independent of T4-01 dokku.md edit. |

Across phases the order is strictly linear: P1 → P2 → P3 → P4.

## Ready for `/speckit:analyze`

Tasks materialised; 14 rows; cap honoured (≤ 25 per Constitution §I.6). Every row cites one FR or `SC-`/`meta`; every row names a passing test or a verification gate. Daemon-side touch forbidden. Next step per CLAUDE.md reliability rule 4: invoke `/speckit:analyze` to cross-check spec / plan / tasks consistency before `/speckit:implement` runs.

## Commit instructions

`.gitignore` blocks `specs/`. Commit:

```bash
cd /Users/notroot/Documents/Code/Apple/wa-147-wa-pair-remote
git add -f specs/110e-wa-pair-remote/tasks.md
git commit -m "tasks(110e): 14-row canonical task list — FR-001..FR-008, 4 phases"
git push
```
