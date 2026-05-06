# Feature 110b — SSE events bridge

**Branch**: `110b-sse-events`
**Status**: implemented (v0 — no Last-Event-ID replay; future
work folds in a ring buffer)
**Source**: spec 110 second sub-PR. 110a shipped the request/
response surface (`POST /v1/rpc`); 110b adds server-pushed events
via Server-Sent Events so REST clients receive the same notifications
the unix-socket `subscribe` method delivers today.

## Problem

After 110a, REST clients can issue RPC calls but cannot subscribe to
events. `wa wait --remote` would return method-not-found because the
REST adapter does not expose the `subscribe` method. SSE is the
canonical fan-out shape for this.

## Decision

Add `GET /v1/events` to the REST adapter, emitting Server-Sent
Events. Plumbing:

1. **`internal/app/eventbridge.go`** gains
   `SubscribeStream(filter, bufSize)` — same shape as
   `RegisterWaiter` but with a configurable buffer (256 default for
   SSE; the existing `RegisterWaiter` keeps cap=1 one-shot semantics
   for `wa wait`).
2. **`internal/app/dispatcher.go`** exposes
   `Dispatcher.SubscribeStream` — a passthrough so the composition
   root can wire the REST adapter to it.
3. **`internal/adapters/primary/rest/dispatcher.go`** adds the
   `EventStream` and `Event` types — local to the rest package so it
   does not import `internal/app`.
4. **`internal/adapters/primary/rest/sse.go`** implements the SSE
   handler.
5. **`cmd/wad/rest_http.go`** declares `restEventStreamAdapter` that
   bridges `*app.Dispatcher` to `rest.EventStream` by translating
   `app.Event` (with a Go-typed Payload) into `rest.Event` (with
   pre-marshalled JSON Data).
6. **`cmd/wad/main.go`** passes the dispatcher through
   `startRESTHTTP(ctx, da, dispatcher, log)`.

### Wire format

Each SSE frame:

```
id: <seq>
event: <type>
data: <json payload>
\n
```

Plus a heartbeat comment frame `: keepalive\n\n` every 25 seconds to
defeat intermediary idle timeouts (nginx default 60s, cloudflare
default 100s — both well above 25s).

Initial frame is `: connected\n\n` so clients know the subscription
established without waiting for the first real event.

### Nginx / proxy behaviour

The REST adapter sets `X-Accel-Buffering: no` on the response. Without
this, nginx queues the entire response in its buffer until the client
disconnects — fatal for SSE. Per the auth+SSE research dossier (PR
109).

### Backpressure

Each SSE subscriber has a 256-event buffer. If a client is slower than
the producer, full buffers DROP the newest event (matches the socket
adapter's `CodeStreamDrop` semantics, FR-063). The client sees missing
events on `id:` jumps and MUST reconcile from history.

### Cancel safety

`restEventStreamAdapter.SubscribeStream` returns a cancel func that
uses `sync.Once` so multiple defers (test helpers, request scope) can
both call it without panicking on `close(stop)`.

### Auth

Identical to `POST /v1/rpc`: `Authorization: Bearer <token>` against
the env-var token from 110a. Missing/wrong token → 401.

### `WithEventStream(events)` server option

`rest.NewServer` accepts an optional `EventStream`. When omitted,
`GET /v1/events` returns 503 with a JSON-RPC error envelope. This
keeps composition opt-in: deployments that need only request/response
can skip the wiring.

## Alternatives rejected

Per Constitution rule 20.

### A. Subscribe semantics over WebSocket

**Rejected** because:

1. The SSE built-in `Last-Event-ID` reconnect handshake matches our
   future ring-buffer cursor design. WebSocket would require custom
   reconnect protocol.
2. Bidirectional surface unused by CLI/agent clients — extra attack
   surface for zero benefit.
3. Dokku's nginx-buildpack disables HTTP/2 on upstream, making
   WebSocket upgrade dance more brittle than SSE over HTTP/1.1.

(This rejection was already documented in spec 110; 110b inherits.)

### B. Per-client ring buffer for Last-Event-ID replay

Add a 1024-event ring to `EventBridge` and replay on
`Last-Event-ID` reconnect. **Rejected for v0** because:

1. Significant refactor of `EventBridge` (atomic seq counter, fan-in
   from multiple producers, replay semantics for slow consumers).
2. Risk of breaking the existing socket subscribe pipeline that has
   no replay today.
3. Operators on a fresh client connection accept "no past events" —
   acceptable v0 limitation.

Future spec (110b v1 or 111) wires the ring with a dedicated
adversarial review.

### C. Long-poll instead of SSE

**Rejected** — no `Last-Event-ID` semantics, reinvents SSE poorly
(PR 109 research dossier).

### D. Reuse `RegisterWaiter` directly with cap=1

**Rejected** — cap=1 channel drops every event under sustained load
(SSE clients buffer multiple events; one-event cap is the wrong
semantics). New `SubscribeStream` method with configurable cap is the
small primitive that matches the use case.

## Functional requirements

- **FR-001** — Authenticated `GET /v1/events` returns 200 with
  `Content-Type: text/event-stream`. Initial frame is
  `: connected\n\n`. Each subsequent published event emits one frame
  in the canonical `id:`/`event:`/`data:` shape, terminated by an
  empty line.
- **FR-002** — Heartbeat comment frame `: keepalive\n\n` emits every
  25 seconds.
- **FR-003** — Server constructed without `WithEventStream` returns
  503 + JSON-RPC error envelope on `GET /v1/events`.
- **FR-004** — Missing/wrong bearer token returns 401 + JSON-RPC
  error envelope.
- **FR-005** — Client disconnect (request context done) deregisters
  the subscriber within 1 second so EventBridge does not accumulate
  stale waiters. Pinned by `TestSSE_ClientDisconnectCancels`.
- **FR-006** — Per-subscriber buffer is 256 events. Full buffer
  drops newest events.
- **FR-007** — Cancel func is idempotent (safe to call multiple
  times). Implemented via `sync.Once` in
  `restEventStreamAdapter.SubscribeStream`.

## Tests

- `internal/adapters/primary/rest/sse_test.go`:
  - `TestSSE_ConnectAndReceive` pins FR-001 via a fake
    EventStream that publishes after the handshake completes.
  - `TestSSE_AuthRequired` pins FR-004.
  - `TestSSE_NoStreamConfigured` pins FR-003.
  - `TestSSE_ClientDisconnectCancels` pins FR-005.

## Out of scope

- **Last-Event-ID replay**: see rejected alternative B. Future
  ring-buffer spec.
- **Per-event filtering on the wire**: client-side filter via a
  query parameter (`?event=message`) — the EventBridge primitive
  already supports filter; the REST handler just hard-wires "all
  events" for v0.
- **Chunked transfer with explicit `event:` for non-event frames**:
  comments-only heartbeat is the standard pattern.

## References

- Spec 109 — Dokku deploy + SSH-forward CLI (parent infra)
- Spec 110 — REST adapter parent design
- Spec 110a — REST request/response surface (`POST /v1/rpc`)
- `internal/app/eventbridge.go:159` — pre-110b `RegisterWaiter`
  one-shot semantics
- Auth+SSE research dossier (PR 109) — nginx X-Accel-Buffering,
  heartbeat cadence, JSON-RPC bridge guidance
