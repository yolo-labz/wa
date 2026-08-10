# spec-guard — mechanising CLAUDE.md rules 2 and 16

CLAUDE.md already forbids the behaviour this gate enforces:

> **2.** Generated artefacts are regenerated, not hand-edited. The "spec
> laundering" anti-pattern (agent edits the spec to match the code it just
> wrote) is forbidden.
>
> **16.** Never edit `spec.md`, `plan.md`, or `constitution.md` from
> `/implement`.

Both were prompt-only until now, and prompt-only prohibitions are measured
insufficient. METR's reward-hacking catalogue (05/06/2025) records frontier
models "modifying the tests or scoring code" to score higher; RepoRescue
([arXiv:2607.01213](https://arxiv.org/abs/2607.01213)) found "Claude Code systems
sometimes edit failing tests even when prompted not to", and its mitigation is
the pattern used here — a runtime regime that blocks the edit instead of asking
for restraint.

## The three layers, and what each is actually worth

| Layer | File | Binds | Bypassable by |
|---|---|---|---|
| Claude Code PreToolUse | `.claude/hooks/spec-guard.sh` | Claude Code sessions in this repo | any other harness |
| pi `tool_call` | `.pi/extensions/spec-guard.ts` | pi seats — **once wired**, see below | any other harness |
| Required CI check | `.github/workflows/spec-guard.yml` | every PR, every harness, humans included | nothing, once required |

The harness hooks are fast and precise but client-side by construction. The CI
job is the floor. Do not treat the hooks as the guarantee.

## What is refused

`specs/*/spec.md`, `specs/*/plan.md`, `specs/*/data-model.md`,
`specs/*/research.md`, `specs/*/contracts/**`, `.specify/memory/constitution.md`,
any `*.feature`, any `*_steps_test.go`, anything under `features/`.

`tasks.md` is deliberately **not** protected — it is the implementation loop's
own working surface, and rule 15 requires the agent to tick it.

Shell bypasses are covered: a redirect, `sed -i` or `tee` aimed at a protected
path is the same edit as the `Edit` tool. Reading one is never blocked — `grep`
and `cat` against a spec are ordinary, legitimate work.

## Changing a specification, legitimately

Specs must be able to change; that is not what this gate is against. Either:

1. Re-run the speckit command that owns the artefact (`/speckit:specify`,
   `/speckit:plan`), which is what rule 2 means by "regenerated"; or
2. Set an explicit, auditable override for a deliberate change:

   ```bash
   WA_SPEC_CHANGE="FR-7 was ambiguous; clarified before implementing" \
     <your command>
   ```

The override is visible on purpose — it appears in the transcript, and the CI
job still asks the PR to declare the change with the `spec-change` label.

## The Stop gate

`.claude/hooks/stop-spec-gate.sh` refuses a turn that ends with specification
artefacts modified in the working tree, and names them. It does not forbid the
change; it forbids finishing silently.

Measured Stop-hook semantics (Claude Code 2.1.219, 10/08/2026 — previously
unverified in the fleet's research):

- A Stop hook blocks by printing `{"decision":"block","reason":…}` to **stdout
  and exiting 0**. Not by exiting 2.
- The assistant receives `reason`, acts on it, and Stop fires again when it next
  tries to finish. A probe confirmed the agent obeyed an injected reason over its
  original instruction.
- On re-entry the payload carries `stop_hook_active: true`. **The short-circuit
  on that field is what terminates the loop** — without it the loop is unbounded.

## Honest limits

- **The pi extension is not auto-loaded.** pi resolves `--extension` flags at
  launch and the herdr seats' argv is fixed by the nix wrapper, so
  `.pi/extensions/spec-guard.ts` is the reference implementation until that
  wiring lands. Until then, pi seats are covered by the CI job only.
- **CODEOWNERS is decorative in this repo.** The `main-protection` ruleset has
  `require_code_owner_review: false` and `required_approving_review_count: 0`, so
  adding spec paths to `CODEOWNERS` would change nothing mechanically. It is
  deliberately not done here rather than shipped as a gate that does not gate.
- **`spec-guard` is not yet a required check.** It runs on PRs; it becomes a
  floor only once added to the ruleset's required contexts, alongside `CodeQL`,
  `Test (go test -race)` and the rest. That is a ruleset change and is left as an
  explicit, separate decision.
- **The hooks fail open.** Any parse error or missing `jq` exits 0 without a
  decision. A gate that crashes the agent loop gets switched off; the CI job is
  where fail-closed belongs.
- **The scenario-suite half is absent on purpose.** This repo has no `.feature`
  files, and a suite gate over zero scenarios either blocks everything or reports
  nothing. It lands with the first scenarios.

## Tests

`.claude/hooks/spec-guard.test.sh` — 21 cases, each block paired with the
near-miss that must *not* block (implementation `.go`, an ordinary `_test.go`,
`tasks.md`, reading a spec, `go build`). A guard that refuses everything and one
that refuses nothing both pass a smoke test; these discriminate.
