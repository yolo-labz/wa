# Feature 110e — Plan: `wa pair --remote <host>:<app>`

**Branch**: `147-wa-pair-remote`
**Worktree**: `/Users/notroot/Documents/Code/Apple/wa-147-wa-pair-remote`
**Spec**: [`spec.md`](./spec.md)
**Status**: planning complete; ready for `/speckit:tasks`
**Estimated PR**: #147

## Technical context

| Item | Value | Source |
|---|---|---|
| Language / toolchain | Go 1.26.2 | `go.mod` |
| New runtime dependencies | **NONE** — `os/exec` from stdlib only | constraint #2 |
| New test dependencies | **NONE** — `rogpeppe/go-internal/testscript` is already in `go.sum` (used elsewhere) | constraint #2 |
| Build invariant | `CGO_ENABLED=0` (unchanged) | repo-wide |
| Target packages | `cmd/wa/` only | spec FR-007 |
| Forbidden packages | `internal/{domain,app,adapters}` | spec FR-007 |

No `[NEEDS CLARIFICATION]` markers carried over from spec. Zero unknowns.

## Module touch list

| Path | Change | Purpose |
|---|---|---|
| `cmd/wa/cmd_pair.go` | **modified** (~12 lines) | Add `pairRemote string` cobra flag; in `RunE`, short-circuit to `runPairRemote` when `pairRemote != ""`. |
| `cmd/wa/cmd_pair_remote.go` | **new** (~80 lines) | `RemoteTarget` struct, `ParseRemoteTarget`, `runPairRemote` (the `exec.Command("ssh", "-t", host, ...)` chain), URL-shape refusal. |
| `cmd/wa/cmd_pair_remote_test.go` | **new** (~120 lines) | Unit tests for parser, URL-refusal, exec-argv shape (via injected `execCommand` indirection — a package-level `var execCommand = exec.Command` swappable in tests). |
| `testscript/cmd_pair_remote.txt` | **new** (~40 lines) | End-to-end CLI script with a stub `ssh` shell script in `$WORK/bin/` that echoes argv to stdout; test asserts the printed argv. |
| `docs/deploy/dokku.md` | **modified** (~25 lines) | New section "Re-pair from a remote workstation" pointing at `wa pair --remote ProxMox.Dokku:wa-burocracy`. |
| `specs/110e-wa-pair-remote/` | **new** | This dir (already committed by spec-PR). |

**Forbidden touches:** anything under `internal/` (per FR-007). Plan's task validator rejects any task whose `files touched` lists an `internal/` path.

## Phases

### P1 — Parser + flag (foundation)

| Step | Output |
|---|---|
| 1. Define `RemoteTarget struct { Host, App string }` in `cmd_pair_remote.go`. | Type declared. |
| 2. Implement `ParseRemoteTarget(s string) (RemoteTarget, error)` — split on first `:`, validate non-empty host + non-empty app, reject `http://` / `https://` prefix with the spec-mandated message. | Parser working with table-driven unit tests. |
| 3. Add `pairRemote string` cobra flag (`--remote`) registered in `cmd_pair.go` `init()`. | `wa pair --help` shows the flag. |

**Exit gate P1:** unit tests for parser pass. `wa pair --help` shows `--remote` line.

### P2 — Exec helper (the SSH wrap)

| Step | Output |
|---|---|
| 4. Define `var execCommand = exec.Command` indirection (test seam). | Single line. |
| 5. Implement `runPairRemote(target RemoteTarget, extraFlags []string) error`: builds `ssh -t <host> dokku enter <app> -- /usr/local/bin/wa pair <extraFlags...>`. Inherits stdin/stdout/stderr. Returns SSH's exit code on failure. | Function compiles. |
| 6. Wire `runPairRemote` into `cmd_pair.go` `RunE` short-circuit (above the existing `callAndClose` path). | `wa pair --remote x:y` routes through the new helper. |
| 7. Implement extra-flag pass-through: collect `--phone`, `--browser`, `--idempotency-key` into the `extraFlags` slice; do NOT pass `--remote` itself (would recurse). | Argv-shape unit test passes. |
| 8. ssh-binary-missing error path: when `exec.LookPath("ssh")` fails, return a `userError{ExitCode: 70, Message: "ssh binary not found in PATH"}`. | Negative test passes. |

**Exit gate P2:** unit tests pass; `wa pair --remote ProxMox.Dokku:wa-burocracy` exec'd locally with a stub `ssh` in `$PATH` prints the expected argv.

### P3 — Tests

| Step | Output |
|---|---|
| 9. Parser unit tests (table-driven): empty input, missing `:`, empty host, empty app, valid forms, `http://`/`https://` rejection. Each row asserts the spec's exit-64 message substring. | 7-row table, all pass. |
| 10. Exec-argv shape test: inject a fake `execCommand` that records `name + args`, assert exact list for the 3 flag combinations (bare, `--phone`, `--browser`). | Pass. |
| 11. ssh-missing test: temporary `PATH=/nonexistent`, assert exit 70 + stderr substring. | Pass. |
| 12. testscript `cmd_pair_remote.txt`: stub `ssh` echoes argv; script asserts exact match. | Pass. |

**Exit gate P3:** `go test -race -count=1 ./cmd/wa/...` green. `go test -race -count=1 ./testscript/...` green.

### P4 — Docs

| Step | Output |
|---|---|
| 13. `docs/deploy/dokku.md` — new section "Re-pair from a remote workstation" with 3 invocations + troubleshooting. | Section landed. |
| 14. `wa pair --help` example block updated (cobra long description) to include `--remote ProxMox.Dokku:wa-burocracy`. | Help reflects new flag. |
| 15. Optional: `README.md` "Operating in production" callout pointing to new doc section. | Optional; YAGNI unless review asks. |

**Exit gate P4:** doc sections render in GitHub Markdown preview; no markdown-lint errors.

## Risk + rollback

- **Surface area:** 1 cobra flag + 1 new `.go` file + 1 testscript + 1 doc section. No daemon, no DB, no protocol.
- **Failure modes considered:**
  - Stub `ssh` argv test fragility under different shell environments — mitigated by writing the stub in pure bash with `#!/usr/bin/env bash` and `printf '%s\n' "$0" "$@"`.
  - Flag conflict with future 110c URL-style `--remote` — already mitigated: pair refuses the URL form (FR-003).
  - Operator running on Windows-without-WSL — out of scope; `wa pair` already documented as POSIX-shell-only in 109.
- **Rollback:** `git revert <merge-sha>` of PR #147. Zero data migrations, zero state, no daemon coordination required. Operators can keep using `ssh -t ... dokku enter ... -- wa pair` until rollback completes — that path is unaffected.

## Constitution check

Walking CLAUDE.md L9-50 rules 1-25. Each rule marked SATISFIED / NOT-APPLICABLE / VIOLATION with one-line evidence.

| Rule | Status | Evidence |
|------|--------|----------|
| 1. Constitution-first | SATISFIED | `.specify/memory/constitution.md` referenced from this plan; spec was constitution-first. |
| 2. Generated artefacts regenerated, not hand-edited | SATISFIED | `plan.md` / `research.md` / `tasks-preview.md` are products of `/speckit:plan`; spec edits flow back to `/speckit:specify`. |
| 3. `/speckit:clarify` before `/plan` | SATISFIED-VIA-EXEMPTION | Spec has ZERO `[NEEDS CLARIFICATION]` markers; clarify step is no-op. |
| 4. `/speckit:analyze` before `/implement` | DEFERRED | Will be run after `/speckit:tasks`. |
| 5. `data-model.md` is field authority | SATISFIED | `data-model.md` declares the single new type `RemoteTarget`; nothing else may reference fields not present there. |
| 6. ≤ 25 tasks per `tasks.md` | SATISFIED | `tasks-preview.md` lists 14 tasks (under cap). |
| 7. Every requirement verifiable by finite check | SATISFIED | Spec FR-001..FR-008 each cite a named test. |
| 8. Specify what, not how | SATISFIED | Spec describes the CLI surface + behaviour. This plan provides "how" but is plan-tier, not spec-tier. |
| 9. Given/When/Then + universal property | SATISFIED-PARTIAL | Spec scenarios provide examples; FR-001..FR-008 carry the universal claims. |
| 10. Read before Write | SATISFIED | `cmd_pair.go` read in earlier tick (visible to caller). |
| 11. `file:line` citations | SATISFIED | Plan cites `cmd/wa/cmd_pair.go`, `internal/adapters/secondary/whatsmeow/pair*.go`. |
| 12. No silent fallbacks | SATISFIED | URL-form refusal exits 64 visibly; ssh-missing exits 70 visibly. No try/except-default-return. |
| 13. No scope creep | SATISFIED | `wa panic --remote` / `wa session logout-all --remote` explicitly OUT OF SCOPE in spec. Plan inherits. |
| 14. Negations are prohibitions | SATISFIED | FR-007 (`MUST NOT change daemon side`) treated as hard prohibition in §"Module touch list" forbidden-touches row. |
| 15. Tests run = `[x]` | SATISFIED-FUTURE | Tasks marked `[x]` only after their named test passes. |
| 16. No spec edits from `/implement` | SATISFIED | Plan flow is one-way: spec → plan → tasks → implement. |
| 17. Challenge wrong premises | SATISFIED | Plan rejected the "URL-style sniff" approach (Alternatives §C in spec) instead of silently accepting it. |
| 18. No "pre-existing" excuse | SATISFIED-FUTURE | Any test failure during implementation is owned, not deferred. |
| 19. CLAUDE.md under 400 lines | NOT-APPLICABLE | This plan does not edit CLAUDE.md. |
| 20. Every architectural decision names ≥ 1 rejected alternative | SATISFIED | `research.md` Decision Records DR-001..DR-005 each name ≥ 1 rejected alternative with reason. |
| 21. Port names = intent, not technology | NOT-APPLICABLE | No new ports added (CLI-only feature). |
| 22. Port set COMPLETE per Cockburn | NOT-APPLICABLE | No new ports. |
| 23. No infrastructure types in port signatures | NOT-APPLICABLE | No port signatures touched. `RemoteTarget` is a CLI-only value type, not a port type. |
| 24. Domain invariants as types/tests | SATISFIED | `RemoteTarget` validation lives in `ParseRemoteTarget` (typed); URL refusal lives in the function (testable). No prose-only invariant. |
| 25. Release engineering per yolo-labz standards | SATISFIED | No release-engineering surface touched; PR will use `git-cliff` changelog + Renovate-pinned actions same as every PR. |

**Net gate result:** PASS. No VIOLATION rows. 5 rules carry DEFERRED / SATISFIED-FUTURE markers tied to implement-tier verification.

## Open questions for `/speckit:tasks`

None. All planning decisions resolved in `research.md` DR-001..DR-005.

## Ready for `/speckit:tasks`

Plan complete. Next step: invoke `/speckit:tasks` to convert this plan into a 14-row `tasks.md` (already previewed at `tasks-preview.md`). The tasks generator must:

1. Cite the spec FR for every row.
2. Name the single passing test per task.
3. Mark `[P]` for parallel-safe rows (parser tests, exec helper tests, docs are independent).
4. Refuse to emit any row whose `files touched` lists an `internal/` path.
5. Refuse to emit any row introducing a Go dependency outside stdlib.

## Commit instructions

`.gitignore` blocks `specs/`. After writing all artefacts, force-add and commit:

```bash
cd /Users/notroot/Documents/Code/Apple/wa-147-wa-pair-remote
git add -f specs/110e-wa-pair-remote/plan.md \
            specs/110e-wa-pair-remote/research.md \
            specs/110e-wa-pair-remote/data-model.md \
            specs/110e-wa-pair-remote/contracts/cli-flag.md \
            specs/110e-wa-pair-remote/quickstart.md \
            specs/110e-wa-pair-remote/tasks-preview.md
git commit -m "plan(110e): wa pair --remote design + Phase-0/1 artefacts"
git push
```
