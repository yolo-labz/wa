# wa — Market-Leadership Roadmap (2026-06)

Goal: make `wa` the best WhatsApp automation tool on the market — the easiest
to use, the easiest to integrate, and the only one safe enough to hand to an
AI agent. This document is the evidence-based plan; each phase lands as its
own spec (`specs/NNN-*`) through the normal speckit flow.

Research base (10/06/2026): full competitive sweep of WAHA, Evolution API,
Baileys, GOWA, wppconnect, whatsapp-web.js, mautrix-whatsapp, wuzapi,
mudslide, green-api, Twilio, Meta Cloud API; MCP ecosystem state
(spec rev 2025-11-25, official Go SDK v1.6.x); DX state-of-the-art
(Standard Webhooks 1.0, OpenRPC, Scalar, n8n verified community nodes).

## Where the market is

| Player | Model | Weakness we exploit |
|---|---|---|
| Evolution API | free self-host, Brazil epicenter, n8n node at ~3M dl/mo | Postgres+Redis required, memory leaks, license/org churn |
| WAHA | Docker "5 min", Swagger-first, 3 engines | Core tier = 1 session, text-only; media/multi-session/**MCP permission scopes paywalled** ($19–99/mo); GOWS engine OOM issues |
| Baileys | de-facto library (~2.3M dl/mo) | library only — everyone rebuilds the same gateway; v7 broke downstreams |
| GOWA | closest Go single-binary peer (REST + MCP) | REST and MCP **mutually exclusive**; no scoping, no audit, basic-auth only; needs external FFmpeg/libwebp |
| lharries/whatsapp-mcp | 5.8k★ household name for "WhatsApp MCP" | **abandoned since 07/2025**, 184 open issues, poster child of MCP exfiltration write-ups (Invariant Labs, Docker) |
| Meta/Twilio/green-api | official rails / SaaS | business verification + template pricing / $24+ per instance/mo — hostile to personal automation |

Two structural facts dominate:

1. **The "WhatsApp MCP server" seat is empty.** The famous one is dead and
   security-infamous; WAHA paywalls the safety controls; GOWA has no safety
   model at all. The defining MCP narratives of 2025–26 are *exfiltration
   incidents*. Safety is the unmet requirement, not a nice-to-have.
2. **No transport is ban-safe anymore** (whatsmeow #810, Baileys #1869,
   wwebjs serial bans). The market publishes hygiene *guides*; nobody
   *enforces* hygiene in the hot path. `wa` already does: default-deny
   allowlist, non-overridable rate limits (2/30/1000) with fresh-session
   warmup ramp, append-only audit log, inbound prompt-injection firewall,
   idempotent sends, human-review draft queue.

`wa`'s positioning, therefore: **the safety-first WhatsApp automation daemon
— the only one you can safely hand to an AI agent, free, in one binary.**
We do not chase Evolution into multi-tenant gateway land; we make the
single-user/personal-automation segment unambiguous ours, and win the
agent era.

## Phase 0 — own the agent era (highest leverage)

### 0.1 First-class MCP server: `wa mcp serve`
- Official `modelcontextprotocol/go-sdk`; stdio + Streamable HTTP transports.
- **~12 workflow-shaped tools, not 76 endpoint wrappers** (per Anthropic
  tool-design guidance): `wa_send_message`, `wa_search_messages`,
  `wa_get_thread`, `wa_resolve_contact`, `wa_list_chats`,
  `wa_schedule_message`, `wa_draft_review`, `wa_send_media`,
  `wa_group_info`, `wa_wait_for_reply`, `wa_transcribe_voice`, `wa_status`.
- `--toolsets messages,contacts,groups` + `--read-only` (GitHub MCP pattern).
- `response_format: concise|detailed` on read tools; names-not-JIDs by
  default (measured ~3× token savings + fewer hallucinations).
- **Every tool call traverses the existing allowlist → rate-limit → audit
  pipeline.** Scoped tokens (spec 110d) gate the HTTP transport. This is
  the headline: free, in-core, non-bypassable — exactly what WAHA charges
  for and lharries never had.
- Errors surface as instructive tool-execution errors with remediation
  commands (SEP-1303), e.g. "recipient not in allowlist; run `wa allow add …`".
- Distribution: `server.json` to the official MCP Registry, `.mcpb` bundle
  for one-click Claude Desktop install, listings on Smithery/Glama/
  PulseMCP/Docker MCP Catalog.

### 0.2 Agent-readable surface (one PR, compounding)
- `llms.txt` + `llms-full.txt` (repo + served by `wad`).
- `docs/errors.json` machine-readable error catalog (code, name, retryable,
  remediation) + RFC 9457 `application/problem+json` on REST.
- `--json` coverage audit: every CLI command, stable schemas, documented
  exit-code contract.
- Consumer-facing agent cookbook ("how an agent drives wa": flags, schemas,
  the allowlist/draft-approve safety dance) + a small agentic eval set to
  tune tool descriptions against.

## Phase 1 — integration table stakes, done better

### 1.1 Outbound webhooks (Standard Webhooks spec)
- Official Go lib; HMAC-SHA256 `webhook-id/timestamp/signature` headers.
- Per-endpoint secrets, topic filtering, exponential-backoff retries
  (~8 attempts/24 h), SQLite dead-letter queue, `wa webhook deliveries
  --failed` + `wa webhook replay <id>`.
- Unlocks the reply-bot pattern and every automation platform that can't
  hold an SSE connection. Adopters of the spec already include OpenAI,
  Anthropic, Supabase — verification snippets work everywhere.

### 1.2 API contract artifacts + docs UI
- OpenAPI 3.1 for the transport endpoints + **OpenRPC document for all
  JSON-RPC methods** (with `rpc.discover` runtime introspection — nobody
  in this market ships it).
- Scalar docs UI embedded in `wad` at `/docs` (matches WAHA's Swagger,
  looks a decade newer, zero hosting).
- `oapi-codegen` Go client now; Speakeasy free-tier TS SDK when demand
  appears.

### 1.3 Five-minute onboarding
- `docker run` one-liner with QR pairing reachable on boot (110e remote-pair
  is the enabler); compose quickstart.
- `install.sh` over GoReleaser checksums (auditable, checksum-verified).
- `wa doctor` already exists — wire it into every failure path.
- Deploy templates: Railway, Coolify (one PR upstream), Easypanel,
  Umbrel/CasaOS. Brew + Nix already done — ahead of every competitor; say so.
- Engineer and track TTFHW (time-to-first-message) per release.

## Phase 2 — distribution channels

### 2.1 n8n community node (`n8n-nodes-wa`)
- The single biggest growth channel in this market: Evolution's node does
  ~3M dl/mo — 230× WAHA's. Verified-node rules: MIT, zero runtime deps,
  one-click install on n8n Cloud since v1.94.
- Credentials = base URL + token; actions = send/sendMedia/search/contacts/
  schedule; trigger = webhooks (depends on 1.1).

### 2.2 Benchmark + trust marketing (cheap, factual)
- Reproducible `bench/`: RSS/session + msgs/sec vs WAHA-WEBJS (their own
  docs: ~20 GB/50 sessions), Evolution (leaks), GOWA. The ~30 MB single
  binary is an order-of-magnitude story.
- "Anti-ban pipeline" naming for the existing warmup/rate/allowlist
  machinery, documented honestly against whatsmeow #810 (no "no blocking"
  snake oil — WAHA's claim is widely disbelieved).
- Days-behind-upstream-whatsmeow badge; SLSA L2 + signed releases as
  procurement argument vs Evolution's license churn.
- PT-BR quickstart (Brazil is the demand epicenter; Evolution's home turf).

### 2.3 Optional humanization flag
- ✅ `--humanize` (PR #243): typing presence → jittered delay → paused
  before send (the canonical hygiene flow, productized). Off by default;
  composes with the rate limiter, never replaces it. Also shipped the
  whatsmeow PresenceAdapter, turning the four `presence.*` methods live in
  production (previously method_not_found — port existed, adapter didn't).
- Deferred: `sendSeen` pre-step needs a last-incoming-message surface
  (FromMe + per-chat latest query) that the history store doesn't expose
  yet — own slice.

## Phase H — hardening (parallel track, from the 018 audit)

Priority subset that protects the positioning claims:
1. **SEC-02**: SSE event payloads must get the `<channel>` prompt-injection
   wrap (the firewall is a headline feature — close the gap).
2. SEC-01/03/04/06: socket dir perms, pair-HTML symlink guard, audit-log
   error-string hygiene, enforce declared `group.*` allowlist actions.
3. Tier-2 ports (groups/privacy/profile/chat-state): finish contract tests —
   they back the MCP group/chat tools.
4. CON-01/02 shutdown races; deterministic clocks in tests (TEST-11/12).

## Sequencing

```
specs/225: MCP server (0.1)                    ← flagship, start now
specs/226: agent-readable surface (0.2)        ← parallel, small
specs/227: standard webhooks (1.1)
specs/228: OpenRPC/OpenAPI + Scalar (1.2)
specs/229: docker one-liner + install.sh (1.3)
n8n-nodes-wa repo (2.1)                        ← after 1.1
bench/ + README comparison table (2.2)         ← any time
SEC-02 fix                                     ← immediate, independent PR
```

Success metrics: MCP Registry listing live; TTFHW ≤ 5 min from
`docker run`; n8n node published + verification submitted; ≥3 deploy
templates; SEC-02 closed; benchmark artifact reproducible in CI.
