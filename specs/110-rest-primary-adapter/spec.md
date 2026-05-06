# Feature 110 — REST primary adapter (HTTPS + bearer auth + SSE)

**Branch**: `110-rest-primary-adapter` (deferred — code lands in a future PR)
**Status**: design-only (no code in this PR)
**Source**: spec 109 follow-up. Stage 1 deploys `wad` on Dokku with
unix-socket-only access via SSH-forwarding. Stage 2 (this spec) adds
a REST primary adapter so non-CLI clients (browsers, mobile,
serverless agents) can drive the daemon without SSH.

This spec **does not ship code in this PR**. It locks the architecture
decisions so future work executes without re-deciding.

## Problem

The SSH-forwarded socket from spec 109 solves multi-host CLI access
but requires:

1. SSH credentials on the Dokku host — fine for an operator's
   workstation, awkward for ephemeral cloud agents.
2. Unix sockets — incompatible with browsers, mobile, and most
   non-Go runtimes.
3. Direct shell access to the host — broader privilege than
   "drive the WhatsApp daemon".

A scoped HTTPS surface with token-based auth is the canonical answer.
The existing hexagonal blueprint already lists
`internal/adapters/primary/rest/` as an anticipated primary adapter
(CLAUDE.md §"Repository layout").

## Decision

### Wire surface

```
POST /v1/rpc           — JSON-RPC 2.0 envelope; mirrors the unix-socket protocol
GET  /v1/events        — Server-Sent Events stream wrapping the existing
                         `subscribe` JSON-RPC method; reconnect via
                         Last-Event-ID header → ring-buffer cursor
GET  /healthz          — already shipped in spec 109; no change
GET  /readyz           — already shipped in spec 109; no change
GET  /v1/version       — daemon version string (unauthenticated)
```

### Auth

**Opaque random 256-bit tokens + sqlite sidecar table.** Stored in a
new `tokens.db` (or extension of `messages.db` v5→v6 migration —
TBD). Tokens are random bytes, displayed once at issue time, persisted
as `sha256(token)` only.

Schema:

```sql
CREATE TABLE tokens (
    token_id      TEXT PRIMARY KEY,           -- ULID, lex-sortable
    hash          BLOB NOT NULL UNIQUE,       -- sha256(raw token)
    name          TEXT NOT NULL,              -- operator-supplied label
    scope         TEXT NOT NULL,              -- "admin" | "send" | "read"
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    last_used_at  INTEGER,
    revoked_at    INTEGER
);
CREATE INDEX idx_tokens_revoked ON tokens (revoked_at);
```

Verification: `sha256(presented_token)` → constant-time lookup
(`subtle.ConstantTimeCompare` after hash). `last_used_at` batched
every 60s to avoid SQLite write-amplification.

### Scopes

| Scope    | Allowed methods                                              |
|----------|--------------------------------------------------------------|
| `read`   | `status`, `health`, `messages`, `thread.get`, `messages.search`, `groups`, `groups.get`, `contacts.lookup`, `contact.resolve`, `media.resolve`, `media.download`, `audit.tail` |
| `send`   | everything in `read` plus `send`, `sendMedia`, `react`, `markRead`, `send.reply`, `chat.composing`, `presence.*`, `message.forward`, `message.star`, `draft.*` |
| `admin`  | everything plus `allow.*`, `pair`, `panic`, `chat.archive`, `chat.mute`, `chat.pin`, `chat.markUnread`, `contact.block`, `contact.unblock`, `privacy.*`, `session.logoutAll`, `profile.*`, `group.*`, `poll.vote`, `message.revoke`, `message.edit`, `schedule.*`, `labels.*`, `embeddings.*` |

Method → scope mapping lives in `internal/adapters/primary/rest/scopes.go`
as a `map[string]Scope` table. Adding a new method without updating
the table fails the build (compile-time check via a `t.Run` over every
dispatcher entry).

### TLS

TLS terminates at Dokku's nginx-buildpack via the `dokku-letsencrypt`
plugin. The daemon speaks plain HTTP behind it. Trust the
`X-Forwarded-For` header only when `RemoteAddr` is `127.0.0.1`/`::1`
(localhost-bound nginx upstream).

### Server-push events

`GET /v1/events` is SSE (`Content-Type: text/event-stream`). Auth
via the same bearer token (Authorization header — NOT
EventSource's cookie-only handshake; clients use `fetch` + manual
SSE parsing OR `eventsource-polyfill` libraries).

Each event is a JSON object identical to today's `subscribe`
notification frames, prefixed with `id: <seq>\ndata: <json>\n\n`.
On reconnect, clients send `Last-Event-ID: <seq>` and the server
replays from the ring buffer — same cursor semantics as the existing
`subscribe` method.

Heartbeat: `: keepalive\n\n` every 25s to defeat intermediary idle
timeouts.

### CLI integration

```bash
wa --remote https://wa.example.com --token $WA_TOKEN status
wa --remote https://wa.example.com --token $WA_TOKEN messages --limit 10
```

Token storage: macOS Keychain / Linux libsecret via
`zalando/go-keyring`, fallback to `$XDG_CONFIG_HOME/wa/auth.yml`
mode 0600. `WA_TOKEN` env var precedence (matches `GH_TOKEN`).

`wa auth login --remote https://wa.example.com` opens an interactive
flow: prompts for the operator-supplied name, generates a token via
`POST /v1/rpc` `token.issue` (using the daemon's localhost-only admin
endpoint OR an out-of-band `wad token issue` subcommand), stores in
keyring.

`wa auth status` lists configured remotes + scope. `wa auth logout`
revokes the local token via `token.revoke`.

### Token admin

```
wad token issue --scope read --ttl 30d --name "operator-laptop"
wad token revoke <token-id>
wad token list
wad token sweep            # garbage-collect revoked tokens older than 30d
```

These run inside the container (`dokku enter`) — they print the
freshly-minted token to stdout, which is captured by the operator.
The token is never logged.

### Rate limiting (defense in depth)

REST adapter applies a per-token coarse limit (100 req/min) **before**
the existing in-app rate limiter. The inner pipeline keeps its
per-JID/per-second caps untouched.

Per-IP limiting at the REST layer is **not applied** because Dokku's
nginx collapses all traffic to one upstream IP (`127.0.0.1`); IP
limits would punish all tokens collectively.

## Alternatives rejected

Per Constitution rule 20.

### A. JWT (HS256 / RS256 / EdDSA) instead of opaque tokens

The classic answer. **Rejected** because:

1. JWT alg-confusion attacks are still surfacing in 2026 audits
   (`alg=none`, `HS256` vs `RS256` confusion). Opaque tokens have no
   header to confuse.
2. JWT revocation requires a denylist anyway; once you have one, the
   "stateless" benefit evaporates.
3. Single-tenant daemon. The "verify across multiple services without
   round-trip" benefit doesn't apply.
4. PASETO v4.local is the modern alternative if signing-based tokens
   are needed; revisit if the daemon ever fans out to multiple verifiers.

### B. WebSocket for events

**Rejected** because:

1. Bidirectional surface our CLI/agent clients don't need — extra
   attack surface for zero benefit.
2. SSE has built-in `Last-Event-ID` reconnect semantics that match
   our existing event ring-buffer cursor; WebSocket needs custom
   reconnect logic.
3. Dokku's nginx-buildpack disables HTTP/2 on upstream connections;
   WebSocket upgrade dance over HTTP/1.1 is more brittle than SSE.
4. SoftwareMill 2026 + Nimbleway 2026 protocol comparisons both put
   SSE ahead of WebSocket for unidirectional fan-out.

### C. mTLS instead of bearer tokens

**Rejected** because:

1. Dokku's nginx-buildpack strips client certificates by default;
   passing them through to the upstream requires bypassing the
   buildpack.
2. Distributing client certs to every CLI install is more friction
   than copying a token string.
3. mTLS does not ship an obvious revocation path without an OCSP
   endpoint; revoke-by-row in the token table is simpler.

### D. gRPC instead of JSON-RPC over HTTPS

**Rejected** because:

1. Adds protoc toolchain dependency to the build pipeline; CLAUDE.md
   §"Decisions already locked in" rejected this for the unix-socket
   IPC for the same reason.
2. The wire format is already JSON-RPC. Re-encoding adds nothing.
3. Browsers cannot speak gRPC natively (gRPC-Web requires a proxy);
   defeats the "non-CLI clients" motivation.

### E. Cookie-based session auth (form login)

**Rejected** because:

1. CLI-first surface — cookies break headless usage.
2. CSRF protection adds complexity (state-changing JSON-RPC methods
   would need a CSRF token).
3. AI agent / serverless callers don't have cookie jars.

## Functional requirements

- **FR-001** — `POST /v1/rpc` accepts a JSON-RPC 2.0 envelope and
  routes through the existing `Dispatcher.Handle` (preserves the
  allowlist + rate-limiter middleware stack).
- **FR-002** — `GET /v1/events` streams SSE. Reconnect via
  `Last-Event-ID` resumes from the event ring buffer cursor.
  Heartbeat every 25s.
- **FR-003** — Authentication via `Authorization: Bearer <token>`
  header. Missing/expired/revoked → 401. Wrong scope for the
  requested method → 403.
- **FR-004** — Tokens are 256-bit random, displayed once at issue,
  stored as `sha256(token)`. Revocation is immediate (next request
  by the revoked token returns 401).
- **FR-005** — `wa --remote <url> --token <tok>` mode reuses the
  existing CLI subcommand tree; only the transport layer differs.
- **FR-006** — Token storage on the client side prefers system
  keyring (Keychain / libsecret) with a `~/.config/wa/auth.yml`
  mode-0600 fallback.
- **FR-007** — `WAD_REST_HTTP_ADDR` env activates the REST adapter
  (analogous to `WAD_HEALTH_HTTP_ADDR` from spec 109). Unset = no
  REST surface, byte-identical pre-spec behaviour.
- **FR-008** — REST adapter applies a per-token coarse rate limit
  (100 req/min default) before the in-app rate limiter; per-IP
  limits are explicitly disabled because Dokku's nginx collapses
  upstream IP.

## Implementation phasing

Three sub-PRs (each ≤25 tasks per Constitution rule 6):

1. **110a** — `internal/adapters/primary/rest/` skeleton: `Server`,
   `Authenticator` interface, `POST /v1/rpc` handler, scopes table,
   tests against `FakeDispatcher`. No SSE yet.
2. **110b** — SSE event bridge: `GET /v1/events`, `Last-Event-ID`
   replay from existing ring buffer, heartbeat, reconnect tests.
3. **110c** — Token admin: `wad token issue|revoke|list|sweep`,
   sqlite table migration, `wa auth login|logout|status` client side,
   keyring integration.

Spec 109 delivered the Dokku container + SSH-forward path, so the
REST adapter has a real deploy target ready when 110a lands.

## References

- Spec 109 — Dokku container + persistent storage + SSH-forward CLI.
- Spec 105 / 106 / 107 / 108 — domain primitives this REST surface
  serializes (LID, IdentityResolver, AddressingMode, server-kind).
- `internal/app/dispatcher.go:298` — entry point the REST handler
  calls into.
- `internal/app/eventbridge.go` — ring buffer + cursor source for SSE
  replay.
- Research dossier (this session, 2026-05-06) — token-tradeoff +
  SSE-vs-WebSocket comparison.
