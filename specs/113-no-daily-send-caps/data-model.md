# Feature 113 — data model and state authority

## State authority summary

Feature 113 removes process-local ordinary-send daily state. It introduces no
entity, persisted field, database table, file, wire field, or schema version.

| State | Authority after Feature 113 | Lifecycle | Persisted? |
|---|---|---|---|
| Ordinary-send second window | Ordinary-send limiter token bucket: rate 2/s, capacity 2 | Constructed at daemon start; refills continuously | No |
| Ordinary-send minute window | Ordinary-send limiter token bucket: rate 30/min, capacity 30 | Constructed at daemon start; refills continuously | No |
| Warmup tier | Multiplier derived from session creation time: 0.25 / 0.50 / 1.00 | Fixed for one limiter instance, recalculated on daemon construction | Session creation time already persists; multiplier does not |
| Allowlist decision | Existing per-profile allowlist authority | Checked before rate limiting | Existing file; unchanged |
| Audit decision | Existing append-only audit authority | One decision per existing send policy path | Existing log; unchanged |
| Idempotency | Existing idempotency authority | Existing load/store/replay lifecycle | Existing store; unchanged |
| Group-create daily budget | Existing group-admin adapter counter: maximum 5 | UTC-day rollover, reserve, refund on upstream failure | No; unchanged |
| Participant-add daily budget | Existing group-admin adapter counter: maximum 50 | UTC-day rollover, reserve, refund on upstream failure | No; unchanged |
| Pending Cafezinho delivery | Existing webhook delivery queue row | pending → delivered or dead; one selected row is replayed | Existing webhook store; operational state only |

## OrdinarySendLimiter

### Fields

| Field | Type/shape | Invariant |
|---|---|---|
| `secondWindow` | Token bucket | Rate 2 tokens/second multiplied by warmup; burst `max(1, floor(2 × warmup))` |
| `minuteWindow` | Token bucket | Rate 0.5 tokens/second multiplied by warmup; burst `max(1, floor(30 × warmup))` |
| `warmup` | One of `{0.25, 0.50, 1.00}` | 0–7 days → 0.25; 8–14 → 0.50; 15+ → 1.00 |

### Explicitly absent fields

- global day token bucket;
- per-recipient daily map;
- unique-new-recipient counter;
- day-start/reset timestamp;
- recipient-history classifier or callback;
- force, disable, override, or configurable limit.

### Transition

For every ordinary send:

1. The existing allowlist decision runs first.
2. The second-window bucket is checked.
3. The minute-window bucket is checked.
4. The existing downstream authorization, idempotency, send, and audit behavior
   continues unchanged.

If a short-window check fails, the existing rate/warmup error is returned. No
transition reads or writes a calendar-day ordinary-send count.

## GroupAdminBudget

This is a separate unchanged state authority, not part of
`OrdinarySendLimiter`.

| Operation | Daily maximum | Rollover | Refund rule |
|---|---:|---|---|
| Create group | 5 | UTC midnight | Upstream failure returns the reserved slot |
| Add participant | 50 participants | UTC midnight | Upstream failure returns all reserved slots |

Removing ordinary-send daily state cannot change either counter. Broadcast
targets remain invalid before any send or group transition.

## Webhook recovery state

The release operates on existing queue state only:

```text
four matching pending rows
        |
        | select maximum updated timestamp
        v
one selected delivery --replay once--> pending/due --> delivered
three older deliveries ------------------------------> not manually replayed
```

Selection and verification may emit identifiers, timestamps, and states only.
Message payload content is not copied into logs, receipts, or spec artefacts.

## Validation rules

- A same-JID count greater than 30 is valid if each send satisfies the short
  windows and independent safeguards.
- A global ordinary-send count greater than 1,000 is valid under the same
  condition.
- Recipient novelty is not limiter state.
- The immediate third send is invalid until the second-window bucket refills.
- A sixth same-day group creation and a 51st same-day participant addition are
  invalid.
- Exactly four matching pending Cafezinho deliveries are required before the
  release replay step; any other count aborts before mutation.

## Migration

**No persisted migration.** The removed ordinary-send fields exist only in the
in-memory limiter (`internal/app/ratelimiter.go:40-53`) and its constructor
(`internal/app/ratelimiter.go:92-101`). No `session.db`, `messages.db`,
`webhooks.db`, audit log, profile file, schema SQL, or migration version changes.

Daemon restart constructs the simplified limiter from the existing session
creation time. Logout, session wipe, and re-pair are forbidden deployment
actions for this feature.
