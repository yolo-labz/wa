# Feature 111 — MCP primary adapter ("the safe WhatsApp MCP server")

**Branch**: `111-mcp-primary-adapter` (design-only — no code in this PR)
**Status**: design locked, implementation staged in §Milestones
**Source**: docs/roadmap.md Phase 0.1 (10/06/2026 market research)

This spec locks the architecture so implementation PRs execute without
re-deciding. It follows the spec-110 precedent: decisions here, code later.

## Problem

Agents are the fastest-growing consumer of messaging automation, and the
"WhatsApp MCP server" seat is empty:

- `lharries/whatsapp-mcp` (5.8k★, the household name) is abandoned since
  07/2025 and is the canonical case study of MCP data exfiltration
  (Invariant Labs and Docker write-ups).
- WAHA ships MCP but paywalls permission scopes (Plus tier); in the free
  tier the agent gets full API access, and media is reachable with any key.
- GOWA runs MCP or REST — mutually exclusive — with no scoping and no audit.

`wa` already owns the primitives the agent era demands — default-deny
allowlist, non-overridable rate limiter with warmup, append-only audit log,
the FR-005a `<channel>` prompt-injection envelope (subscriber-wired by
SEC-02/PR #227), idempotent sends, and a human-review draft queue. What is
missing is the MCP transport that lets agent runtimes consume them.

CLAUDE.md §Mission previously delegated MCP to the `wa-assistant` plugin.
That stays true for the *Claude Code channel UX*; this spec adds the
protocol-level MCP surface in-core because (a) the safety pipeline must sit
BELOW the agent boundary, in-process, non-bypassable, and (b) every other
MCP client (Cursor, VS Code, ChatGPT, Gemini CLI) can't use a Claude plugin.

## Decision

### Placement: one app-layer service, two primary adapters

```
internal/app/mcptools/        — tool implementations (orchestrate dispatcher methods)
internal/adapters/primary/mcp — transport adapter (official modelcontextprotocol/go-sdk)
cmd/wa/cmd_mcp.go             — `wa mcp serve` (stdio bridge → daemon socket)
cmd/wad (rest_http.go)        — Streamable HTTP endpoint mounted at /mcp on the REST listener
```

- **stdio transport**: `wa mcp serve` runs per-client (spawned by Claude
  Desktop/Cursor), speaks MCP on stdio, and forwards every tool call as
  JSON-RPC over the existing unix socket. The daemon stays the single
  policy enforcement point.
- **Streamable HTTP transport**: mounted on the existing REST listener
  (`WAD_REST_HTTP_ADDR`) at `/mcp`, behind the same bearer-token auth.
  Scoped tokens (spec 110d: read/send/admin) gate tool visibility:
  read-scope tokens see only read tools.
- Both transports run **simultaneously with every other adapter** — the
  daemon multiplexes primary adapters by design (GOWA's REST-xor-MCP
  limitation is an architecture bug we don't have).
- SDK: official `modelcontextprotocol/go-sdk` (stable v1.x, Google-co-
  maintained). Spec rev target: 2025-11-25.

### Tools: 12 workflow-shaped tools, not 76 endpoint wrappers

Per Anthropic tool-design guidance (few consolidated workflow tools beat
endpoint mirrors). Initial set, grouped by toolset:

| Toolset | Tool | Wraps (dispatcher methods) |
|---|---|---|
| messages | `wa_send_message` | send / send.reply / markRead |
| messages | `wa_search_messages` | messages.search (FTS5/hybrid) |
| messages | `wa_get_thread` | thread.get + contact resolution |
| messages | `wa_wait_for_reply` | wait (event filter) |
| messages | `wa_send_media` | sendMedia / media.resolve |
| messages | `wa_schedule_message` | schedule.send/list/cancel |
| messages | `wa_transcribe_voice` | media.download --transcribe |
| contacts | `wa_resolve_contact` | contacts.search/lookup/resolve |
| contacts | `wa_list_chats` | chat.list + last-active |
| groups | `wa_group_info` | groups.get / roster |
| safety | `wa_draft_review` | draft.list/get/approve/reject |
| meta | `wa_status` | status / health / sync.status |

Conventions (locked):

1. **Names, not JIDs, by default.** Read tools resolve JIDs through the
   contact mirror; raw JIDs available under `response_format: "detailed"`.
   `response_format: "concise"` is the default (measured ~3× token savings).
2. **`--toolsets messages,contacts,groups,safety` + `--read-only`** flags
   on both transports (GitHub MCP server pattern). `--read-only` exposes
   only read tools regardless of toolset selection.
3. **Untrusted text crosses the boundary only inside the FR-005a
   `<channel>` envelope** — tool results reuse the SEC-02 subscriber
   projections. An MCP client never sees an unwrapped inbound body.
4. **Errors are instructive tool-execution errors** (SEP-1303), never bare
   codes: `-32100` → "recipient not in allowlist; the operator can run
   `wa allow add <jid> --actions send`". Mapping table maintained in
   `internal/app/mcptools/errors.go`, generated from the domain error
   catalog.
5. **Every tool call traverses the existing pipeline** — allowlist →
   rate limiter → audit — because tools call dispatcher methods; nothing
   is reimplemented above the policy layer. The audit record gains a
   `via: "mcp"` origin field.

### Safety mode: draft-gated sends (the differentiator)

`wa mcp serve --send-mode {direct|draft|deny}`:

- `direct` — sends go out immediately (subject to allowlist + rate caps).
- `draft` (**default**) — `wa_send_message`/`wa_send_media` create entries
  in the existing draft queue; the human approves via `wa draft approve`
  (or any UI built on it). The tool returns the draft ID + status so the
  agent can poll `wa_draft_review`.
- `deny` — send tools are not registered at all (read-only agent).

This turns the existing draft queue into the headline feature: agent
proposes, human disposes — non-bypassable, audited. No competitor has it.

### Resources & prompts

- Resources (read-only context): `wa://chats/recent`, `wa://contact/{jid}`,
  `wa://status` — cheap, cacheable; spec'd loosely, implemented after tools.
- Prompts: `catch_me_up` (summarize unreplied chats since N hours),
  `draft_reply` (compose reply in thread context). Deferred to M3.

### Distribution (after M1 ships)

- `server.json` published to the official MCP Registry (mcp-publisher CLI).
- `.mcpb` bundle for one-click Claude Desktop install.
- Listings: Smithery, Glama, PulseMCP, Docker MCP Catalog.
- README section + comparison table (scoping/audit/draft-gate vs
  WAHA/GOWA/lharries).

## Non-goals

- Multi-tenant gateway features (Evolution/WAHA land) — out of scope.
- OAuth 2.0/CIMD remote auth — bearer tokens suffice for the
  localhost/Tailscale deployment model; revisit only if a public-exposure
  use case materializes.
- MCP sampling/elicitation — not needed for v1.
- Replacing `wa-assistant` — the plugin remains the Claude-Code-native UX.

## FRs

- FR-111-01: `wa mcp serve` speaks MCP 2025-11-25 over stdio; all tool
  calls forward to the daemon socket; daemon down → actionable startup
  error naming `wad` + socket path.
- FR-111-02: Streamable HTTP at `/mcp` on the REST listener, gated by the
  110d token store; token scope filters the registered tool set
  (read → read tools only; send → +send tools; admin → all).
- FR-111-03: `--toolsets` / `--read-only` / `--send-mode` per §Decision;
  defaults: all toolsets, read-write, `draft`.
- FR-111-04: inbound text in tool results is FR-005a-wrapped; fuzz corpus
  extended to the MCP result path.
- FR-111-05: every tool invocation produces an audit record with
  `via:"mcp"`, the tool name, and the underlying method(s).
- FR-111-06: policy refusals surface as tool-execution errors with
  remediation text; protocol errors reserved for transport faults
  (SEP-1303 conformance).
- FR-111-07: `wa_send_message` in draft mode returns
  `{draftId, state:"pending_review"}`; idempotency keys propagate so agent
  retries cannot double-create drafts.
- FR-111-08: tool descriptions + schemas pass the agentic eval set
  (5–10 scripted tasks under `internal/app/mcptools/evals/`) in CI —
  descriptions are tuned against measured task success, not vibes.

## Milestones

- **M1**: stdio transport + messages/contacts toolsets + draft-gate +
  audit origin. Ships usable Claude Desktop/Cursor integration.
- **M2**: Streamable HTTP + scoped-token filtering + groups/safety/meta
  toolsets + registry/.mcpb distribution.
- **M3**: resources + prompts + eval harness in CI + marketplace listings.

## Risks

- MCP spec velocity: pin go-sdk minor; the 2025-11-25 rev is stable and
  LF-governed — churn risk is acceptable.
- Tool-count creep: 12 is a budget, not a floor to fill. New tools need
  an eval-set scenario justifying them.
- Draft-mode friction: agents that expect fire-and-forget sends will need
  the polling pattern documented in the tool description itself (the
  description IS the contract an LLM reads).
