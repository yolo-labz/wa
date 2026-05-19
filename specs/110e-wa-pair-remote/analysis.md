# Feature 110e — Cross-artefact analysis

Constitution rule 4 / §I.4 — `/speckit:analyze` before `/speckit:implement`. Manual analysis (speckit infra absent in this repo; `.specify/scripts/` empty).

## Coverage matrix

| Spec FR | Plan phase | Tasks row | Named test | Status |
|---|---|---|---|---|
| FR-001 (`--remote` exec ssh chain) | P1 + P2 | T1-01, T1-03, T2-02, T2-03 | `TestParseRemoteTarget` + `TestRunPairRemote_ArgvShape` + `TestPairRouting_RemoteWinsOverSocket` + testscript | COVERED |
| FR-002 (parser exit 64 on malformed) | P1 | T1-02 | `TestParseRemoteTarget` (table rows) | COVERED |
| FR-003 (refuse URL form) | P1 + P3 | T1-02, T3-03 | `TestPairRemote_URLFormRefused` | COVERED |
| FR-004 (--browser passthrough) | P2 | T2-02, T2-03 | `TestRunPairRemote_ArgvShape` (--browser row) | COVERED |
| FR-005 (--phone + --idempotency-key passthrough) | P2 | T2-02, T2-03 | `TestRunPairRemote_ArgvShape` (table rows) | COVERED |
| FR-006 (ssh missing → exit 70) | P2 | T2-04 | `TestRunPairRemote_SSHMissing` | COVERED |
| FR-007 (daemon untouched) | P3 verification | T3-04 | `git diff origin/main -- internal/` empty gate | COVERED |
| FR-008 (backwards compat) | P2 | T2-03 | `TestPairRouting_RemoteWinsOverSocket` + existing pair tests still green | COVERED |

| Success criterion | Where addressed |
|---|---|
| SC-001 (30 s re-pair) | Operator-side, validated by quickstart manual smoke (not CI-gated) |
| SC-002 (zero daemon-side changes) | T3-04 verification gate |
| SC-003 (help text discoverability) | T1-03 + T4-03 |
| SC-004 (docs in dokku.md) | T4-01 |
| SC-005 (backwards compat) | FR-008 chain |

## Consistency checks

| Check | Result |
|---|---|
| Every FR has a test → row → file touch | PASS |
| Every task cites one FR (or SC/meta) | PASS |
| `tasks.md` row count ≤ 25 (Constitution §I.6) | PASS (14 ≤ 25) |
| No row edits `internal/` | PASS |
| No row adds non-stdlib Go dep | PASS |
| `data-model.md` entity referenced in tasks | PASS — `RemoteTarget` named in T1-01 |
| Contracts (`cli-flag.md`) every flag column referenced in tasks | PASS — `--remote` in T1-03, interaction matrix in T2-03 |
| Quickstart scenarios covered | PASS — 3 invocations all reach T4-01 doc + T1-03 help |
| Out-of-scope items NOT smuggled into tasks | PASS — no `wa panic --remote` / `wa session logout-all --remote` row |

## Constitution check (re-run post-tasks)

Same result as `plan.md` §"Constitution check": 0 VIOLATION rows, 5 DEFERRED / SATISFIED-FUTURE tied to implement-tier.

## Risks surfaced

- T3-02 testscript fixture argv assertion fragility — `printf '%s\n' "$0" "$@"` emits one arg per line; the test must compare line-by-line, not single-string. Note added to T3-02 description.
- T4-03 cobra long-description edit must avoid duplicating help text that T1-03 already places; merge into single coherent block. Note added to T4-03.

## Status

**PASS**. Ready for `/speckit:implement`. No spec / plan / tasks edits required.
