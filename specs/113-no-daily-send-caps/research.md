# Feature 113 — research and decisions

**Status**: complete; zero unresolved clarifications  
**Decision time**: 28/08/2026 17:00 BRT  
**Evidence basis**: reproduced production failure plus current source and tests

## DR-001 — ordinary sends have short windows, not daily budgets

**Decision**: remove the global daily, per-recipient daily, and
unique-new-recipient daily controls from ordinary outbound sends. Retain
2/second burst 2, 30/minute burst 30, and warmup scaling on both.

**Rationale**: the current limiter contains all three disputed controls:

- global 1,000/day constants and bucket (`internal/app/ratelimiter.go:19-20`,
  `internal/app/ratelimiter.go:43`, `internal/app/ratelimiter.go:96`,
  `internal/app/ratelimiter.go:126-128`);
- a 30/day per-JID map and refusal (`internal/app/ratelimiter.go:23`,
  `internal/app/ratelimiter.go:49`, `internal/app/ratelimiter.go:154-168`);
- a 15/day new-recipient counter and recipient-history callback
  (`internal/app/ratelimiter.go:24`, `internal/app/ratelimiter.go:30-34`,
  `internal/app/ratelimiter.go:50-52`, `internal/app/ratelimiter.go:171-189`).

The production evidence identifies the 30/day counter as the first decisive
error: upstream trigger, filter, context, prompt, model, and cleanup succeeded;
only the outbound send was refused. Pedro explicitly removed the product
premise for all ordinary-send daily budgets.

**Alternatives rejected**:

- Raise 30/day to a larger per-recipient value. Rejected because it postpones
  the same outage and contradicts “no daily cap.”
- Keep the global 1,000/day bucket as a final safety net. Rejected because it is
  still a daily ordinary-send cap and the retained short windows already bound
  immediate runaway behavior.
- Make daily caps configurable or add a privileged bypass. Rejected because a
  configurable zero/force path creates divergent safety contracts and violates
  the non-overridable middleware rule.

## DR-002 — delete recipient-specific limiter plumbing at the shared choke point

**Decision**: reduce the ordinary-send limiter to two token buckets plus warmup.
Remove the recipient-specific `AllowFor` path, `KnownRecipientFunc`,
`SetKnownRecipientFunc`, daily maps/counters, reset time, and the composition
callback. `SafetyPipeline.Check` keeps its JID for the allowlist decision, then
invokes the recipient-independent limiter.

**Rationale**: `SafetyPipeline.Check` is the shared authorization choke point
and currently delegates to `AllowFor` (`internal/app/safety.go:20-27`). The only
dispatcher setter simply forwards to the limiter
(`internal/app/dispatcher.go:399-404`), and the composition root is the only
caller (`cmd/wad/main.go:556-562`). Deleting this chain fixes all ordinary-send
callers once, including the shared scheduled-send pipeline wired at
`cmd/wad/main.go:439-456`.

The per-recipient benchmark exists only to exercise the map branch
(`internal/app/ratelimiter_bench_test.go:39-54`) and becomes dead with that
branch. The ordinary `Allow` benchmark remains relevant.

**Alternatives rejected**:

- Leave `AllowFor(jid)` as a pass-through compatibility wrapper. Rejected: it
  is internal-only, has one production caller, falsely communicates
  recipient-specific policy, and preserves unnecessary benchmark/API surface.
- Leave the known-recipient callback dormant. Rejected: it retains a database
  read and lifecycle coupling for a policy that no longer exists.
- Patch only the Cafezinho caller. Rejected: every send path shares the same
  defect; the shared limiter is the root cause.

## DR-003 — deterministic tests must falsify the removed mechanisms

**Decision**: add two `testing/synctest`-driven regressions before changing the
implementation:

1. 31 sends through `SafetyPipeline.Check` to the same allowlisted JID, spaced
   two virtual seconds apart;
2. 1,100 calls to the ordinary limiter, also spaced two virtual seconds apart.

Retain the existing immediate-third refusal tests
(`internal/app/ratelimiter_test.go:11-28`,
`internal/app/safety_test.go:52-71`).

**Rationale**: two-second pacing stays below both retained windows. The first
test fails the former 30/day per-recipient behavior on call 31. The second is
large enough to fail the former global bucket even after refill: over 2,198
seconds, the old 1,000/day rate earns about 25.44 additional tokens, far below
the 1,100 required. Virtual time keeps both tests deterministic and fast; the
repository already proves this timing pattern with `synctest`
(`internal/app/ratelimiter_test.go:211-236`).

**Alternatives rejected**:

- Inspect struct fields instead of behavior. Rejected because implementation
  shape can change while a hidden daily refusal remains.
- Use wall-clock sleeps. Rejected because a 1,100-send test would be slow and
  flaky; time-sensitive tests are required to use `synctest`.
- Send 1,001 messages. Rejected because refill during legal pacing could let
  the old bucket admit that count, so it would not reliably falsify the former
  implementation.

## DR-004 — group-administration daily state is a separate authority

**Decision**: do not modify group-administration counters or tests.

**Rationale**: group creation and participant addition have distinct adapter
state and constants (`internal/adapters/secondary/whatsmeow/groupadmin.go:28-60`).
Their reserve functions enforce 5/day and 50/day independently
(`internal/adapters/secondary/whatsmeow/groupadmin.go:415-430`,
`internal/adapters/secondary/whatsmeow/groupadmin.go:487-503`). Deterministic
tests already pin the sixth create and 51st add refusals
(`internal/adapters/secondary/whatsmeow/groupadmin_test.go:41-78`,
`internal/adapters/secondary/whatsmeow/groupadmin_test.go:201-238`).

**Alternatives rejected**:

- Move group counters into the simplified ordinary-send limiter. Rejected:
  group administration is a distinct high-risk operation with its own budget.
- Remove all daily counters uniformly. Rejected: Pedro's decision is scoped to
  ordinary messages and explicitly retains the group limits.

## DR-005 — no persisted migration

**Decision**: perform no database or filesystem migration.

**Rationale**: all ordinary-send daily state is held in the in-memory
`RateLimiter` struct (`internal/app/ratelimiter.go:40-53`) and initialized in its
constructor (`internal/app/ratelimiter.go:92-101`). Removing it changes no
session, history, webhook, audit, or schema file. Group-admin counters are also
in-memory and remain intact (`internal/adapters/secondary/whatsmeow/groupadmin.go:49-60`).

**Alternatives rejected**:

- Add a migration that clears daily counters. Rejected because no persisted
  ordinary-send counter exists.
- Reset the session or re-pair after deployment. Rejected because the session
  is unrelated state and destructive recovery would add risk without effect.

## DR-006 — living documentation and generated mirrors change together

**Decision**: update all current-facing claims in one implementation PR:

- public/operator: `README.md`, `README.pt-BR.md`, `SECURITY.md`,
  `docs/manual.md`, `docs/roadmap.md`;
- agent/packaging: `internal/agentdocs/llms.txt`, its root `llms.txt` mirror,
  and `mcpb/manifest.tmpl.json`;
- marketing/generated visual: `.github/og-card.html`, regenerated
  `.github/og-card.png`, and `docs/assets/wa-demo.cast`;
- current audit ledgers: `issues.md` and `research.md`;
- source comment: `cmd/wad/history_methods.go` where the removed callback is
  cited.

**Rationale**: the embedded agent copy is canonical and the root copy is
byte-checked (`internal/agentdocs/agentdocs.go:1-7`,
`internal/agentdocs/agentdocs_test.go:71-87`). The MCP bundle manifest is
generated from its template (`scripts/build-mcpb.sh:23-25`,
`scripts/build-mcpb.sh:49-57`). The OG PNG is generated from the HTML source,
so changing only text sources would leave published output stale.

Historical Feature 016 and Feature 009 artefacts remain immutable audit
records. The v2.0.0 amendment marks their daily-limit assumptions superseded;
they are not living product documentation and are not regenerated from Feature
113.

**Alternatives rejected**:

- Update only README. Rejected because agents, bundles, manual users, and the
  marketing card would continue receiving a false contract.
- Rewrite historical feature specifications. Rejected because that launders
  the historical decision instead of recording its explicit supersession.

## DR-007 — controlled merge, canary, and one-delivery recovery

**Decision**: stop `wa-deploy-sync.timer` before merge, merge only after green
CI and exact-head different-family review, deploy the merge SHA manually to
`wa-burocracy`, require app-level progress, then deploy `wa-personal`. Stop the
Cafezinho replay timer before it can race the manual recovery. After personal
is healthy, use the existing runtime selector to assert four matching pending
rows, replay exactly its newest selection once, verify `delivered` and the
normal workflow branch, then remove/cancel both replay artefacts.

**Rationale**: transport-up is not sufficient; the health contract exposes an
inbound event timestamp plus `staleState` (`internal/app/method_health.go:31-49`,
`internal/app/method_health.go:100-115`). The deploy runbook already requires a
canary and fresh inbound timestamp (`docs/deploy/dokku.md:160-163`). Webhook
delivery listing is newest-first (`internal/adapters/secondary/sqlitewebhooks/webhooks.go:155-166`),
and replay re-arms exactly one identifier (`internal/app/webhook_methods.go:172-190`).

**Alternatives rejected**:

- Let the continuous-deploy timer roll both services. Rejected because it
  removes the bureaucracy-first observation gate.
- Replay all four pending deliveries. Rejected because it creates duplicate or
  stale group replies; the explicit recovery instruction is newest-only.
- Treat `connected:true` as canary proof. Rejected because socket health can be
  green while inbound application progress is stalled.
- Roll back by re-tagging or mutating the session. Rejected: rollback is one
  revert PR deployed in the same bureaucracy-first order.

## Review routing

The implementation is public OpenAI-authored work. The independent exact-head
review seat therefore uses full `zai/glm-5.3:max` (different family), never a
small/Flash model and never a same-family GPT reviewer. Any repair changes HEAD,
so the review and required gates repeat on the new exact SHA.
