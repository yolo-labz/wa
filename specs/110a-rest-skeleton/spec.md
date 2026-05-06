# Feature 110a — REST primary adapter skeleton

**Branch**: `110a-rest-skeleton`
**Status**: implemented
**Source**: spec 110 first sub-PR. The parent spec 110 designed the
full REST adapter (POST /rpc + SSE events + sqlite tokens + wa --remote
client). 110a ships the auth + RPC handler scaffolding so subsequent
sub-PRs (110b SSE, 110c tokens) build on a working skeleton.

## Problem

Spec 109 deployed `wad` on Dokku with unix-socket-only access, plus
SSH-forwarding via `scripts/wa-remote`. SSH-forward unblocks operator
laptops but is awkward for browser/mobile/agent clients. The hexagonal
blueprint (CLAUDE.md §"Repository layout") already lists
`internal/adapters/primary/rest/` as an anticipated primary adapter;
spec 110 designed it; this PR (110a) lands the minimum viable surface.

## Decision

Single-process `internal/adapters/primary/rest/` package mirroring the
shape of `internal/adapters/primary/socket/`. v0 surface:

```
POST /v1/rpc        — JSON-RPC 2.0 envelope; auth required
GET  /v1/version    — adapter version + schema; unauthenticated
```

Reuses the same `Dispatcher.Handle(ctx, method, params)` contract the
socket adapter already calls. The use case layer is unchanged — the
allowlist, rate limiter, audit log, and per-method dispatch all run
exactly as they do for socket clients. Auth happens at the REST edge
ONLY; the inner pipeline does not know it is being driven over HTTP.

### Auth (v0)

**Single env-var bearer token.** `WAD_REST_TOKEN` carries a 256-bit
random secret (operator generates with `openssl rand -hex 32`). The
authenticator hashes it once at startup; every request hot-path
hashes the presented token and constant-time-compares against the
stored digest. Defeats timing oracles via `subtle.ConstantTimeCompare`.

The single-token mode is intentionally minimal — spec 110c will
introduce a sqlite-backed multi-token store with scopes
(`read`/`send`/`admin`) and revocation. 110a treats the env-var token
as a fully-privileged "admin" token; full scope enforcement is out of
scope here.

### Activation

Both env vars MUST be set. Failure modes:

| `WAD_REST_HTTP_ADDR` | `WAD_REST_TOKEN` | Behaviour |
|----------------------|------------------|-----------|
| unset                | unset            | no listener, byte-identical pre-spec-110a |
| unset                | set              | no listener (token without a port is a no-op) |
| set                  | unset            | **REFUSES TO START** — fails closed; would otherwise expose an unauthenticated daemon |
| set                  | set              | REST listener active |

The third row is the load-bearing safety property: a misconfigured
deploy that sets the address without the token MUST NOT silently
expose the daemon. The composition root returns a fatal error from
`run()` and the daemon exits non-zero.

### Wire envelope

Identical to the socket adapter. JSON-RPC 2.0 with `jsonrpc=2.0`,
`method`, `params`, and `id`. Errors come back inside an `error`
object on HTTP 200 (typed dispatcher errors carry `RPCCode()` per the
existing `codedError` interface). HTTP 4xx is reserved for protocol-
layer failures (auth, malformed envelope, oversized body).

### Body cap

1 MiB via `http.MaxBytesReader`. Mirrors the socket adapter's
`CodeOversizedMessage` cap. Defends against memory-exhaustion via
crafted Content-Length.

### Headers

Always set `Content-Type: application/json` + `X-Content-Type-Options:
nosniff` on responses. Reject inbound POST without
`Content-Type: application/json` with HTTP 415. No support for cookies
or query-string auth (both rejected per spec 110 §"Alternatives
rejected E").

## Alternatives rejected

Per Constitution rule 20.

### A. Embed `creachadair/jrpc2/jhttp.Bridge`

The library ships an `http.Handler` that already speaks JSON-RPC 2.0
over HTTP. **Rejected** because:

1. Our existing dispatcher has middleware (allowlist, rate limiter,
   audit log) that runs in `Dispatcher.Handle`. `jhttp.Bridge` would
   call out to a separate jrpc2 server, bypassing the middleware
   stack we already trust.
2. We need full control over auth headers, body-size limits, and the
   reverse-proxy header trust posture — the bridge wraps these with
   defaults we would have to override.
3. Implementing the handler ourselves is ~150 lines and mirrors the
   socket adapter shape, keeping the two primary adapters
   architecturally aligned.

### B. Per-method scope tables in 110a

We could land the full `read`/`send`/`admin` scope table now.
**Rejected** because:

1. The scope table needs the sqlite tokens store (110c) to be
   useful — the env-var single-token mode forces "admin" anyway.
2. Splitting the scope-vs-store work into 110c keeps each PR
   ≤25 tasks per Constitution rule 6.
3. The scope check is purely additive over the auth check — adding
   it later does not break any existing flow.

### C. mTLS on port 443 directly (no reverse proxy)

Skip Dokku's nginx-buildpack and have `wad` terminate TLS itself.
**Rejected** because:

1. mTLS adds a substantial surface (cert validation, OCSP, expiry
   handling) for zero immediate benefit.
2. Dokku's nginx-buildpack already terminates HTTPS via letsencrypt;
   we ride that path instead of reimplementing it.
3. Spec 110 §"Alternatives rejected C" already documented mTLS
   rejection at the parent design level.

## Functional requirements

- **FR-001** — `POST /v1/rpc` with a valid bearer token, valid
  JSON-RPC envelope, and `Content-Type: application/json` reaches
  `Dispatcher.Handle(ctx, method, params)`. The result is encoded
  in a JSON-RPC response envelope with matching `id`. HTTP 200.
- **FR-002** — Missing, malformed, or wrong bearer token yields
  HTTP 401 with a JSON-RPC error envelope (`code = -32099`,
  `message = "unauthorized"`).
- **FR-003** — `NewEnvTokenAuth("")` produces a disabled
  authenticator that rejects every request with `ErrUnauthorized`.
  Misconfigured callers fail closed.
- **FR-004** — Setting `WAD_REST_HTTP_ADDR` without `WAD_REST_TOKEN`
  causes `run()` to return a non-nil error and the daemon exits
  non-zero. Tested via `cmd/wad/rest_http_test.go` (in-process).
- **FR-005** — Request bodies > 1 MiB return HTTP 413 with a
  JSON-RPC error envelope (`code = -32004`).
- **FR-006** — Typed dispatcher errors (carrying `RPCCode()`)
  propagate the code into the JSON-RPC response envelope (HTTP 200,
  `error.code` set).
- **FR-007** — `GET /v1/version` returns 200 + JSON
  `{"schema":"wa.rest.version/v1","adapter":"rest-110a"}` with no
  authentication. Lets clients verify they're talking to the right
  daemon before sending a token.

## Tests

- `internal/adapters/primary/rest/server_test.go` — 9 test cases:
  happy path, three auth-fail variants, disabled-auth fails-closed,
  four envelope-validation cases (malformed JSON, missing jsonrpc,
  wrong jsonrpc version, missing method), typed dispatcher error,
  oversized body, version probe, nil-dispatcher rejected,
  nil-auth rejected.

## Out of scope

- **`GET /v1/events` SSE** — spec 110b.
- **Sqlite-backed token store + `wad token` admin** — spec 110c.
- **`wa --remote <url> --token <tok>` CLI mode** — spec 110c.
- **Per-method scope enforcement** — spec 110c (when multi-token
  store lands).
- **Reverse-proxy `X-Forwarded-For` trust** — spec 110b/c (not
  needed for v0 since we're behind Dokku nginx and don't audit
  IP today; revisit when audit logs include client IP).

## References

- Spec 109 — Dokku container + persistent storage + SSH-forward CLI.
- Spec 110 — REST adapter parent design (this PR's umbrella).
- `internal/adapters/primary/socket/` — pattern this adapter mirrors.
- `internal/app/dispatcher.go:298` — `Handle` entry point.
