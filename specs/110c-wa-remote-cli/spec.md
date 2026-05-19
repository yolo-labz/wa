# Feature 110c v0 — `wa --remote` CLI mode

**Branch**: `110c-wa-remote-cli`
**Status**: implemented (v0 — single-token mode; multi-token sqlite
admin deferred)
**Source**: spec 110 third sub-PR. 110a shipped the daemon-side REST
adapter with env-var bearer-token auth; 110c v0 adds the client-side
counterpart so operators can drive `wad` from any machine that can
reach the HTTPS endpoint.

## Problem

After 110a, the daemon exposes `POST /v1/rpc` over HTTPS but every
existing `wa` subcommand is hard-wired to the unix socket transport
via `callAndClose(flagSocket, ...)`. Without a CLI counterpart, the
REST surface is only reachable via raw curl — operators want
`wa send --remote https://wa.example.com --to ...` to just work.

## Decision

Add two global persistent flags + a new HTTP transport. ZERO changes
to subcommand bodies — every existing `callAndClose(flagSocket, ...)`
call site remains untouched, but `callAndClose` now routes to the
HTTP transport when `flagRemote != ""`.

### Surface

```
wa --remote https://wa.example.com --token $WA_TOKEN status
wa --remote https://wa.example.com --token $WA_TOKEN messages --limit 10
wa --remote https://wa.example.com --token $WA_TOKEN send --to 5511999999999 --body "hi"
```

`--remote` is the URL of the daemon. `--token` is the bearer token,
falling back to the `WA_TOKEN` env var when unset (matches `gh`'s
`GH_TOKEN` precedence).

### Transport routing

`callAndClose` (`cmd/wa/rpc.go:120`) checks `flagRemote != ""` first.
When set, dispatches to `callRemote` (new `cmd/wa/transport_http.go`).
The unix-socket dial path is untouched and remains the default.

### URL normalisation

`https://wa.example.com` → `https://wa.example.com/v1/rpc`
`https://wa.example.com/` → `https://wa.example.com/v1/rpc`
`https://wa.example.com/v1/rpc` → unchanged

### Plaintext refusal

`http://` URLs are refused unless `WA_REMOTE_INSECURE=1` is set.
Operator must explicitly acknowledge that they accept plaintext over
the wire — useful for local-loopback testing only. Defends against
accidental "wa --remote http://wa.example.com" leaking tokens to a
network adversary.

### Exit-code mapping

| Condition | Exit |
|-----------|------|
| HTTP 200 + `result` | 0 |
| HTTP 200 + `error` envelope | `rpcCodeToExit(env.Error.Code)` (matches socket transport) |
| HTTP 401 unauthorized | 64 (usage error) |
| HTTP 4xx other | 64 |
| HTTP 5xx | 70 (internal) |
| dial failure / timeout | 10 (service unavailable) |
| transport error | 1 |

Identical to the socket transport's mapping for the cases that overlap
(`rpcCodeToExit` is reused), divergent only at the HTTP-layer rows
where the socket transport has no equivalent.

### Body cap

16 MiB ceiling on the response read via `io.LimitReader`. Defends
against memory exhaustion if the daemon were ever to return an
oversized response. Matches the daemon's 1 MiB request cap pattern,
intentionally larger on the client side because `messages` and
`history` responses can be substantial.

### Timeout

60-second context deadline on the HTTP request. Long enough for the
slowest current dispatcher method (`groups`, `history` with FTS5),
short enough to fail fast on a hung daemon.

### Token resolution precedence

1. `--token <tok>` flag
2. `$WA_TOKEN` env var
3. empty (request goes out without `Authorization` header → daemon
   returns 401, surfaces as exit 64 with actionable message)

## Alternatives rejected

Per Constitution rule 20.

### A. Reuse `--socket` flag with URL-shaped values

Detect URL prefix in `flagSocket` and route accordingly. **Rejected**
because:

1. `--socket` is documented and used as a path; overloading its
   meaning breaks shell completion + scripts that grep for the flag.
2. Two distinct concepts (local socket path vs remote URL) deserve
   distinct flags. Operators should not have to mentally substitute
   "well, when this looks like a URL it actually means..."
3. Help text becomes a contradiction: "path to wad unix socket" while
   accepting URLs.

### B. Implement as a new subcommand `wa remote <verb>`

Make `--remote` an explicit subcommand instead of a global flag.
**Rejected** because:

1. Forces every existing subcommand to be re-implemented under
   `remote`, doubling the maintenance burden.
2. Breaks the "any subcommand against any transport" affordance —
   operators want `wa --remote ... messages` to just work, not
   `wa remote messages`.
3. Cobra global flags cleanly handle this without special routing.

### C. Embed an HTTP/2 client + reuse connection pool

Use `golang.org/x/net/http2` for connection multiplexing across
calls. **Rejected** because:

1. Each `wa` invocation is a single short-lived process that issues
   one or two RPC calls; connection reuse across invocations needs
   a daemon-mode CLI, which is far out of scope.
2. The default `http.DefaultClient` (HTTP/1.1) is enough.
3. Adds a transitive dep we do not need.

## Functional requirements

- **FR-001** — `normaliseRemoteURL("https://host")` returns
  `https://host/v1/rpc`. Trailing-slash variants and full-path
  variants normalise to the same canonical form.
- **FR-002** — `callRemote(url, token, method, params)` issues a
  POST to `<normalised-url>` with `Authorization: Bearer <token>`,
  body = JSON-RPC envelope, and decodes the response identically to
  the socket transport.
- **FR-003** — When `flagRemote` is set, `callAndClose` dispatches
  to `callRemote` and `flagSocket` is ignored. When unset, the
  socket dial path runs unchanged (no behaviour change for existing
  users).
- **FR-004** — Plaintext `http://` URLs are refused with a clear
  error unless `WA_REMOTE_INSECURE=1` is set.
- **FR-005** — Token resolves from `--token`, then `$WA_TOKEN`,
  then empty. Empty token causes the daemon to return 401, mapped
  to exit 64.
- **FR-006** — HTTP 401 → exit 64, HTTP 5xx → exit 70, dial failure
  → exit 10, dispatcher error envelope → `rpcCodeToExit(envelope
  .Error.Code)`. Mapping documented in the table above.
- **FR-007** — `[]byte` params (used by callers passing pre-marshalled
  JSON) are embedded verbatim into the request body, not
  base64-encoded — matches the existing socket-transport behaviour.

## Tests

`cmd/wa/transport_http_test.go`:
- `TestNormaliseRemoteURL` — 7 URL-canonicalisation cases incl.
  plaintext-refusal and invalid-input rejection.
- `TestCallRemote_HappyPath` — auth + result envelope decode + exit 0.
- `TestCallRemote_AuthFails` — 401 → exit 64 with actionable message.
- `TestCallRemote_DispatcherError` — 200 + error envelope propagates
  to `*rpcError` and rpcCodeToExit.
- `TestCallRemote_5xxFails` — internal error → exit 70.
- `TestCallRemote_ConnectFails` — dial failure → exit 10.
- `TestCallRemote_InvalidEnvelope` — malformed JSON response → exit 1.
- `TestCallRemote_RawParamsEmbedded` — pins []byte params embed
  verbatim.

Total: 7 cases + 1 table-driven URL test (7 sub-cases) = 14 effective
assertions on the new surface.

## Out of scope

- **Sqlite multi-token store** — daemon-side. Deferred to spec 110c-v2.
  Today the daemon uses a single env-var token (110a). Per-token
  scopes (`read`/`send`/`admin`) become enforceable when the store
  lands.
- **`wad token issue|revoke|list|sweep` admin subcommands** — depend
  on the sqlite store.
- **`wa auth login|logout|status` client-side** — keychain integration
  via `zalando/go-keyring`. Operator today supplies `--token` directly
  or via `WA_TOKEN` env. Keychain is convenience, not safety.
- **SSE event streaming via `wa wait --remote`** — depends on spec 110b.
  Today `wa wait` over `--remote` returns method-not-found because
  the REST adapter does not expose `subscribe` (nor does it have an
  SSE handler).
- **HTTP/2 connection multiplexing** — see rejected-alternative C.

## Verification

- `go test -race -count=1 ./cmd/wa/...` full suite green.
- `golangci-lint run ./...` 0 issues.
- Cross-transport regression: every existing CLI subcommand passes
  its golden-file tests with `flagRemote == ""` (default unchanged).

## References

- Spec 109 — Dokku container + SSH-forward CLI
- Spec 110 — REST adapter parent design
- Spec 110a — REST adapter skeleton (`POST /v1/rpc`, env-var token)
- Spec 110e — extends this with `wa pair --remote <host>:<app>` for SSH-keyed pair (pair flow does not fit REST transport; see DR-003 in 110e/research.md).
- `cmd/wa/rpc.go:120` — `callAndClose` entry point
- `cmd/wa/exitcodes.go` — `rpcCodeToExit` mapping reused by HTTP transport
