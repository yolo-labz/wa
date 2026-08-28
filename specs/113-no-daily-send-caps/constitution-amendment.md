<!--
Sync Impact Report
==================
Version change: 1.1.0 -> 2.0.0
Rationale: MAJOR. Principle V.26 required an ordinary-send per-day limit. Pedro
explicitly removed that invariant on 28/08/2026 at 17:00 BRT. The amended
principle forbids global, per-recipient, and unique-new-recipient daily limits
for ordinary sends while retaining non-overridable short-window limits and the
separate group-administration daily limits.

Modified principles:
  - V.26 Rate limiter as non-overridable middleware (redefined)

Added sections:
  - Reviewable authority for the ignored generated constitution mirror

Removed requirements:
  - Ordinary-send global per-day budget
  - Ordinary-send per-recipient daily budget
  - Ordinary-send unique-new-recipient daily budget

Tracked propagation in the same reviewable PR:
  - CLAUDE.md
  - README.md, README.pt-BR.md, SECURITY.md, docs/manual.md, docs/roadmap.md
  - llms.txt and internal/agentdocs/llms.txt
  - mcpb/manifest.tmpl.json, .github/og-card.{html,png}, and
    docs/assets/wa-demo.cast
  - issues.md and research.md historical/supersession rows

Local-only file:
  - .specify/memory/constitution.md is ignored by .gitignore. It is a generated
    convenience mirror and MUST NOT be edited by this feature. Regenerate it
    later from this tracked amendment plus tracked CLAUDE.md; it cannot override
    either reviewable source.

Deferred metadata:
  - The ignored mirror's LAST_AMENDED_DATE must be set to the eventual merge
    commit author date when that mirror is regenerated; it is not guessed here.
-->

# wa Constitution Amendment v2.0.0 — no daily ordinary-send caps

**Status**: ratified and implemented by this change; merge pending
**Decision**: 28/08/2026 17:00 BRT
**Supersedes on merge**: Constitution v1.1.0 Principle V.26 and Feature 009's
ordinary-send daily-limit assumptions

## Delivery decision

The amendment and root fix ship in one PR. A separate docs-only amendment merge
would trigger this repository's continuous production deployment before the
implementation existed, adding a second two-daemon rollout while Cafezinho was
already unavailable. The amendment and authoring artefacts were completed
before the Go change; one reviewable diff keeps policy, tests, implementation,
and living documentation atomic.

## Reviewable authority

The repository ignores `.specify/`, including its local constitution mirror
(`.gitignore:38`). A policy change recorded only in that path cannot participate
in branch review. The tracked `CLAUDE.md` and tracked amendment records under
`specs/` are therefore the reviewable authority. The ignored mirror is generated
working state: it MAY be regenerated from tracked policy, but MUST NOT override
this amendment or `CLAUDE.md`.

## Amended Principle V.26

**Rate limiter as non-overridable middleware.** Every ordinary outbound send
MUST pass both short-window controls:

- at most 2 sends per second, with burst 2;
- at most 30 sends per minute, with burst 30.

Fresh-session warmup MUST preserve the implemented age boundaries: 25% while
session age is less than 7 days, 50% from age 7 days through less than 14 days,
and 100% from age 14 days onward. Every scaled burst MUST remain at least one.

Ordinary outbound sends MUST NOT have a global daily cap, a per-recipient daily
cap, or a unique-new-recipient daily cap. No CLI flag, environment variable,
configuration value, RPC, or internal callback may force, bypass, or disable
the short-window controls.

The distinct group-administration safeguards remain mandatory: at most 5 group
creations per day, at most 50 participant additions per day, and no broadcast
lists.

## Preserved independent safeguards

This amendment changes only ordinary-send daily accounting. It does not relax
the default-deny allowlist, append-only audit decisions, idempotency, delivery
eligibility checks, draft review, prompt-injection envelopes, warmup, short
windows, group-administration daily limits, or broadcast refusal.

## Rationale

The reproduced Cafezinho incident completed its trigger, filtering, context,
prompt, model, and cleanup work, then lost only the outbound reply to a daily
rate refusal. The pre-amendment implementation contained a 1,000/day global
bucket, a 30/day per-recipient counter, and a 15/day unique-new-recipient counter
(`internal/app/ratelimiter.go:19-24`, `internal/app/ratelimiter.go:43-52`,
`internal/app/ratelimiter.go:126-188`). The 30/day counter caused the observed
outage. Pedro's decision is that ordinary message volume has no daily budget.

The 2/second and 30/minute windows still stop immediate runaway loops. The
other independent safeguards continue to constrain who may receive a message,
how retries behave, which administrative actions are permitted, and what is
audited.

## Compliance checks

1. At least 31 paced ordinary sends to one allowlisted JID succeed.
2. A deterministic paced sequence large enough to exhaust the former global
   1,000/day bucket succeeds.
3. A third immediate send after two accepted sends is rejected.
4. Warmup scales both remaining short windows and cannot be bypassed.
5. Existing 5/day group-create and 50/day participant-add tests remain green.
6. Ordinary-send daily state and the recipient-history callback are absent.
7. Living public, operator, agent, marketing, and packaging documentation has
   no ordinary-send daily-cap claim after implementation.
