# Feature 112 — standard-webhooks outbound delivery

**Branch**: `232-webhooks` (design + code in one PR)
**Status**: shipped with this spec
**Source**: docs/roadmap.md Phase 1.1

## Problem

Push beats SSE-polling for the integrations that drive adoption in
this market (n8n trigger nodes, Home Assistant, serverless): a
receiver should get signed HTTP POSTs it can verify with one library
call, not hold a long-lived authenticated stream.

## Decision

- **Spec compliance**: Standard Webhooks v1 (official Go library).
  HMAC-SHA256 over `id.timestamp.payload`; `webhook-id`,
  `webhook-timestamp`, `webhook-signature` headers. Receivers verify
  with any standard-webhooks library — OpenAI/Anthropic/Supabase use
  the same scheme.
- **Storage**: `webhooks.db` (sqlitewebhooks adapter, sqliteschedule
  layout). Endpoints carry a per-endpoint `whsec_…` secret (returned
  exactly once at add; stored for signing — the daemon must be able to
  re-sign retries) and a comma-separated topic list (`*` wildcard).
- **Delivery**: DB-backed queue — restart-safe, no in-memory timers.
  Worker subscribes to the EventBridge (subscriber projections only:
  attacker text stays inside the FR-005a channel envelope), fans out
  one delivery row per (event, matching endpoint), and a 5s scan loop
  POSTs due rows. Payload: `wa.webhook/v1` envelope
  `{schema, deliveryId, topic, ts, data}`.
- **Retry/backoff**: 8 attempts over ~23h (30s → 12h), then state
  `dead`. 5 consecutive dead deliveries auto-disable the endpoint
  (`disable_reason` recorded). `webhook.replay` re-arms any delivery
  (including dead) for an immediate attempt.
- **Methods + scopes**: `webhook.add`/`webhook.remove` = **admin**
  (an endpoint is a data-egress destination); `webhook.list`/
  `webhook.deliveries` = read; `webhook.replay` = send. CLI:
  `wa webhook add|list|rm|deliveries|replay`.
- **Error**: `-32118 webhook_not_found` (catalogued in errors.json).

## Non-goals

- Inbound webhooks (the daemon initiates; it does not receive).
- Per-delivery operator UI — `wa webhook deliveries --state dead` +
  `replay` is the solo-operator-sized surface.
- mTLS / IP allowlisting on receivers — operators front their own
  receivers; admin-scoped `webhook.add` is the egress gate.
