# AGENTS.md — reviewer rules for `yolo-labz/wa`

Enforcement checklist for the Codex adversarial reviewer (and any agent
reviewing a PR in this repo). The *why* lives in [`CLAUDE.md`](./CLAUDE.md)
rules 26–32 and [`docs/reliability.md`](./docs/reliability.md); this file is
*what to flag*. Each gate maps to a CLAUDE.md rule and is **PR-blocking**.

These rules are incident-driven: a `wa-burocracy` ~12 h silent inbound stall and
an undiagnosable REST dispatcher panic (2026-06-01), plus the PR #595
speculative-fix near-miss. Review as if a real person's WhatsApp is on the line —
because it is.

## Daemon-reliability gates

### Real-liveness — CLAUDE.md R26
- FLAG any health / readiness / liveness check that asserts only **transport**
  state (`IsConnected()`, `connected:true`, keepalive / ping round-trip) with no
  **inbound-delivery / app-progress** signal. Socket-up is a green lie under
  WhatsApp server-side device demotion (keepalive validates the socket, never
  auth; logout/demotion arrives as a stream error, not a ping failure).
- REQUIRE connection-health surfaces to express **soft-stale** ("connected, no
  inbound past threshold") as a first-class state, derived from the last-inbound
  timestamp — not synthesised from the socket.

### Watchdog / auto-heal — CLAUDE.md R27
- For code that auto-acts on a health signal, REQUIRE all of: **edge-triggered**
  (`healthy→stale` transition, not level), **opt-in** (explicit env/flag;
  detect-only default), **cooldown-bounded**, **reversible** (touches only the
  live socket — no `Logout`, no `session.db` mutation, no QR re-pair), and a
  **synctest test** proving "fires exactly once per edge + cooldown honoured."
- REJECT a self-heal that mutates session/pairing state. REJECT level-triggered
  action loops (reconnect-storm risk).
- Auto-heal is permitted ONLY for a narrow / reversible / well-understood failure
  class. Anything else MUST be **detect-and-alert** — emit, let a human decide.

### Diagnosable before fix — CLAUDE.md R28
- REQUIRE every `recover()` to log `string(debug.Stack())` plus method/context —
  not just the panic value. A recover that logs only `fmt.Sprintf("%v", rec)` is
  itself a defect (an undiagnosable crash in the recovery path).
- ACCEPT a diagnosability-only PR (stack capture, no root fix) as a complete,
  shippable deliverable. Do not demand the root fix in the same PR.

### No speculative fix — CLAUDE.md R29
- REJECT a root-fix PR that cannot cite a **reproduction** OR a **concrete
  defect** (named `file:line` — e.g. an unguarded nil). Ask for the diff of the
  surface that would have to change; an **empty diff-surface ⇒ speculative ⇒
  instrument-and-wait**, not merge.
- REQUIRE an honest **✓ / ◐ / ◯** status in the PR body. REJECT `✓ verified`
  claimed for work that is mechanism-only (◐) or circumstantial (◯).

### First decisive error — CLAUDE.md R30
- On a multi-symptom change, ask: is this the **first unrecoverable step**, or
  downstream noise? Block PRs that patch the loudest / most-repeated line while
  the upstream cause stands.

### Concurrency tests — CLAUDE.md R31
- REQUIRE `go test -race -shuffle=on -count=1` for any concurrency change.
- REQUIRE `testing/synctest` (`synctest.Test`) with `net.Pipe` / in-memory fakes
  for time- or scheduling-sensitive tests. FLAG real network I/O or mutex blocks
  inside a synctest bubble — they are not durably blocking, so `synctest.Wait`
  will not advance virtual time and the test is wrong.
- REQUIRE `go.uber.org/goleak` coverage for new long-lived daemon goroutines
  (watchdog, history-sync worker, event bridge).

### Deploy = daemon-safety — CLAUDE.md R32
- For any reconnect / self-heal / session / health change, REQUIRE a documented
  **rollback** and a **canary `wa-burocracy` before `wa-personal`** plan in the
  PR body. Green CI is necessary, not sufficient — the change ships to a human's
  live account. Never re-tag a release (provenance binds to the signing SHA).

## Merge ownership — CLAUDE.md R33

- **Done = merged, not PR-opened.** The agent that opens a PR owns it through
  merge. Do not park a green, in-class PR with "awaits your merge."
- **Self-merge the SAFE class** — CI-green AND review-clean AND in-class
  (code/docs/config, ≤1 service, revertible by one PR) — via
  `gh pr merge <n> --squash --delete-branch`. Drive it there first:
  `BEHIND → gh pr update-branch <n>`; `DRAFT → gh pr ready <n>`; poll
  `gh pr checks <n>` to green (`--auto` is not enabled on these repos).
- **Escalate ONLY** (tag `[pending] Pedro` with the exact unblock action):
  prod-deploy/migration, Actions/CODEOWNERS/release-tags,
  irreversible/destructive, blast-radius >1 service,
  MFA/secret/hardware/billing, `nh os switch`, or a required approval you
  genuinely cannot self-satisfy.
- **NEVER** `--admin`, `--no-verify`, force-push, or direct push to `main`. If a
  required approval blocks merge and `--admin` is the only way through, that is
  an escalation, not a workaround.
- A `wad` reconnect/self-heal change is incident-class but still self-mergeable
  once CI is green (`-race -shuffle` clean) + reviewed; only the prod **deploy**
  and the GHCR-public **exposure** stay Pedro-gated.

## General hygiene
- Conventional-commit PR title (lefthook enforces); scope is a **single token**
  (no commas — `fix(rpc)`, not `fix(rest,socket)`).
- gofumpt-clean; `depguard` hexagonal boundary intact (no infra types in port
  signatures); Renovate SHA-pins preserved with their `# vX.Y.Z` comments.
- Cite `file:line` for every factual claim about the codebase (CLAUDE.md R11).
