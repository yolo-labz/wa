# Feature 113 — no daily ordinary-send caps

**Branch**: `358-no-daily-send-caps`  
**Status**: specified; implementation and release pending  
**Product decision**: Pedro, 28/08/2026 17:00 BRT  
**Constitution**: [v2.0.0 amendment](./constitution-amendment.md)

## Problem

Ordinary WhatsApp conversations currently stop after a calendar-day budget is
consumed even when traffic remains within the immediate anti-burst limits. The
reproduced Cafezinho incident completed every upstream workflow stage, then
lost only the outbound reply to a rate-limit refusal after 30 accepted sends to
the same destination. Four Cafezinho webhook deliveries were pending at the
time of diagnosis; their message content was not printed.

Pedro's product decision is unambiguous: ordinary message sending has no global
daily cap, no per-recipient daily cap, and no unique-new-recipient daily cap.
Daily volume must not turn a healthy conversational workflow into an hours-long
outage.

This decision does not remove immediate anti-burst controls or the independent
safety controls around authorization, auditability, replay, group
administration, and broadcast refusal.

## Scope

An **ordinary send** is any one-to-one or group message send, regardless of
whether it originates from an interactive command, an approved draft, a
scheduled action, an agent tool, or another supported transport. Every such
path shares the same ordinary-send safety decision.

This feature:

- removes all calendar-day accounting from ordinary sends;
- retains 2 sends/second with burst 2 and 30 sends/minute with burst 30;
- retains fresh-session warmup on both short windows;
- retains allowlist, audit, idempotency, delivery checks, draft review, the
  5 group-create/day and 50 participant-add/day limits, and broadcast refusal;
- removes recipient-history coupling that exists only to classify daily
  new-recipient volume;
- corrects every living public, operator, agent, marketing, and packaging claim
  about ordinary-send limits;
- releases through a controlled two-service canary and restores exactly one
  pending Cafezinho delivery.

## User scenarios and testing

### User Story 1 — continue a legitimate conversation (Priority: P1)

As an operator or approved automation, I can continue sending ordinary messages
throughout the day as long as each send satisfies the short-window and
independent safety checks.

**Independent test**: send 31 ordinary messages to the same allowlisted JID,
paced at one every two seconds on a virtual clock. Every send is accepted. A
second deterministic sequence of 1,100 equally paced sends is also accepted.

**Acceptance scenarios**:

1. **Given** a mature session, one allowlisted JID, and 30 earlier accepted
   ordinary sends that day, **When** the 31st send is paced within both short
   windows, **Then** it is accepted. **Universal property**: for every finite
   sequence that satisfies the short windows and other independent safeguards,
   acceptance is invariant under the count of earlier same-day sends to that
   recipient.
2. **Given** ordinary traffic paced within both short windows, **When** the
   accepted count grows beyond the capacity of the former global daily budget,
   **Then** no daily refusal occurs. **Universal property**: for every two
   otherwise-identical histories that differ only in same-day global ordinary
   send count, the next-send decision is identical.
3. **Given** any set of previously unseen allowlisted recipients, **When** an
   ordinary send to the next recipient satisfies the remaining safeguards,
   **Then** it is not refused because of how many new recipients appeared that
   day. **Universal property**: recipient novelty and same-day unique-recipient
   cardinality never enter an ordinary-send authorization decision.

### User Story 2 — keep immediate and administrative brakes (Priority: P1)

As the account owner, I retain protection against rapid loops, unsafe targets,
administrative abuse, duplicate execution, and broadcasts after daily ordinary
send accounting is removed.

**Independent test**: two immediate mature-session sends pass and the third is
refused; existing warmup, allowlist, audit/idempotency, group-create, participant
add, and broadcast-refusal checks remain green.

**Acceptance scenarios**:

1. **Given** a full-warmup limiter with unused capacity, **When** three sends are
   attempted without elapsed refill time, **Then** exactly the first two pass
   and the third is refused. **Universal property**: no interval of one second
   admits more than two ordinary sends, including an initial burst.
2. **Given** traffic spread across a minute, **When** the minute capacity is
   exhausted, **Then** further sends wait for refill. **Universal property**:
   no rolling minute admits more than the configured initial burst plus tokens
   earned at the 30/minute rate.
3. **Given** a session in any warmup tier, **When** an ordinary send is checked,
   **Then** both short-window rates and bursts use the tier multiplier and each
   burst remains at least one. **Universal property**: the multiplier is exactly
   25% on days 0–7, 50% on days 8–14, and 100% from day 15 onward, with no
   override path.
4. **Given** a non-allowlisted target or a duplicate idempotency key, **When** a
   send is attempted, **Then** the existing refusal or replay result is
   preserved and audited. **Universal property**: removing daily accounting
   changes neither authorization order nor idempotent outcome for any request.
5. **Given** group-administration traffic, **When** a sixth group creation or
   51st participant addition is attempted in one day, **Then** it is refused;
   broadcast lists remain forbidden. **Universal property**: ordinary-send
   volume never changes either group-administration daily budget or broadcast
   refusal.

### User Story 3 — publish one consistent safety contract (Priority: P2)

As a user or integrating agent, I see the same ordinary-send limits in every
living documentation surface and generated copy.

**Independent test**: a tracked-text scan finds no living claim of a global,
per-recipient, or unique-new-recipient daily ordinary-send cap; intentional
5/day and 50/day group-administration claims remain.

**Acceptance scenario**: **Given** any current public, operator, agent,
marketing, or packaging description of the safety pipeline, **When** it lists
ordinary-send limits, **Then** it states 2/second burst 2 and 30/minute burst 30
with no daily ordinary-send cap. **Universal property**: every byte-identical
mirror matches its canonical source, and generated visual or package output
contains the same contract.

### User Story 4 — recover Cafezinho without duplicate replies (Priority: P1)

As the operator, I can land and deploy the fix deliberately, prove real
application progress on the canary, and replay only the newest of the four
pending Cafezinho deliveries.

**Independent test**: release receipts prove deployment automation was stopped,
the bureaucracy canary advanced its inbound application timestamp, personal was
deployed second, exactly one selected delivery reached `delivered`, the normal
reply path ran instead of the fallback path, and both replay artefacts are gone.

**Acceptance scenarios**:

1. **Given** continuous deployment automation is active, **When** the change is
   ready to merge, **Then** that automation is stopped before merge and both
   services are deployed manually in order. **Universal property**: no release
   step may advance personal before bureaucracy has `staleState=healthy` and a
   strictly advancing inbound application timestamp.
2. **Given** four pending Cafezinho deliveries, **When** personal is healthy,
   **Then** the newest matching delivery is replayed exactly once and the older
   three are not manually replayed. **Universal property**: selection is the
   maximum pending update timestamp among matching Cafezinho triggers and one
   release run invokes replay for exactly that identifier.
3. **Given** the selected replay, **When** processing settles, **Then** its state
   is `delivered` and the corresponding workflow execution uses the normal
   response path rather than the fallback path. **Universal property**: a
   release is incomplete unless both queue settlement and downstream reply-path
   evidence refer to the selected delivery.
4. **Given** replay completion, **When** cleanup finishes, **Then** the replay
   timer is absent or cancelled and its runtime script is absent. **Universal property**:
   no scheduled or filesystem replay artefact remains capable of
   sending one of the older deliveries later.

## Functional requirements

- **FR-001**: Ordinary sends MUST have no global daily limit.
- **FR-002**: Ordinary sends MUST have no per-recipient daily limit.
- **FR-003**: Ordinary sends MUST have no unique-new-recipient daily limit and
  MUST NOT depend on recipient-history classification.
- **FR-004**: Ordinary sends MUST retain 2 sends/second with burst 2 and
  30 sends/minute with burst 30 across every supported send entry point.
- **FR-005**: Fresh-session warmup MUST scale both short-window rates and bursts
  to 25% on days 0–7, 50% on days 8–14, and 100% from day 15, with minimum
  burst one.
- **FR-006**: Allowlist, audit, idempotency, delivery checks, draft review, and
  authorization order MUST retain their existing behavior.
- **FR-007**: Group administration MUST retain 5 creations/day and
  50 participant additions/day; broadcast lists MUST remain forbidden.
- **FR-008**: No force, bypass, disable, or configuration control may weaken the
  ordinary-send short windows or warmup.
- **FR-009**: Every living safety-contract publication and generated mirror
  MUST describe the amended limits consistently.
- **FR-010**: The release MUST pass the race-enabled shuffled test suite, lint,
  native build, Nix checks/build, and an exact-head different-family review.
- **FR-011**: Continuous deployment MUST be stopped before merge; rollout MUST
  canary bureaucracy first using app-level liveness, then personal.
- **FR-012**: The release MUST replay exactly the newest of the four pending
  Cafezinho deliveries, prove `delivered` plus a normal non-fallback reply, and
  remove or cancel both replay artefacts.
- **FR-013**: Rollback MUST be one revert PR deployed to bureaucracy first and
  personal second, with the same app-level liveness gate.

## Edge cases

- A burst of three remains a refusal even if no message has been sent earlier
  that day.
- A mature session that has sent more than 1,000 messages still obeys the
  short windows; it does not gain burst capacity from the removed daily limit.
- A new JID must still pass allowlist and delivery eligibility. “No
  unique-new-recipient daily cap” is not permission to send to unknown targets.
- Scheduled and draft-approved sends share the same short-window budget as
  interactive sends; no entry point becomes an implicit bypass.
- Group-administration counters remain daily even though ordinary-send counters
  do not.
- If the pending Cafezinho set is not exactly four at release time, replay stops
  before mutation and the discrepancy is investigated; selection is not guessed.
- If canary liveness does not advance, personal is not deployed and no replay is
  attempted.

## Success criteria

- **SC-001**: 31 paced ordinary sends to one allowlisted JID succeed in a
  deterministic held-out test.
- **SC-002**: 1,100 ordinary sends paced one every two virtual seconds succeed,
  a sequence that the former 1,000/day bucket cannot admit even with refill.
- **SC-003**: Two immediate sends succeed and the immediate third fails.
- **SC-004**: All existing warmup, allowlist/audit/idempotency,
  group-create/day, participant-add/day, and broadcast-refusal tests pass.
- **SC-005**: A source scan finds no ordinary-send daily state or
  recipient-history callback in the active implementation.
- **SC-006**: A living-document scan finds no current ordinary-send daily-cap
  claim, and canonical/mirror agent documentation is byte-identical.
- **SC-007**: All required local/CI/Nix gates and exact-head independent review
  are green, and the PR state is `MERGED` with a merge receipt.
- **SC-008**: Bureaucracy reports healthy app-level liveness with an advancing
  inbound timestamp before personal deployment begins.
- **SC-009**: Exactly one Cafezinho delivery—the newest of the four pending—is
  replayed, reaches `delivered`, and produces a normal non-fallback reply.
- **SC-010**: The replay timer is absent/cancelled, the runtime replay script is
  absent, and the rollback receipt identifies one revert PR and bureaucracy-
  first deployment order.

## Assumptions

- “Day” remains relevant only to the independent group-administration budgets.
- Ordinary-send limiter state remains process-local and ephemeral; no persisted
  counter or schema migration is introduced.
- The four pending Cafezinho rows are operational incident state, not fixture
  data and not content to copy into repository artefacts.
- Historical feature specifications remain an audit trail. The tracked v2.0.0
  amendment supersedes their old daily-limit assumptions; living documentation
  is updated instead of rewriting history.

## Out of scope

- Raising, replacing, or making the short-window limits configurable.
- Adding a force flag, privileged bypass, broadcast support, or bulk-send API.
- Changing group-admin limits or their accounting.
- Changing persisted schemas, message history, session pairing, logout, or
  recovery behavior.
- Editing implementation or living public/operator/agent documentation during
  this specification-authoring pass.
- Deploying, replaying, sending messages, or mutating services during this pass.
