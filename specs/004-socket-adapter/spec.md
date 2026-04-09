# Feature Specification: Socket Primary Adapter (JSON-RPC 2.0 over Unix Socket)

**Feature Branch**: `004-socket-adapter`
**Created**: 2026-04-08
**Status**: Draft
**Input**: User description: "Socket primary adapter: line-delimited JSON-RPC 2.0 server over unix domain socket. Exposes wad use cases to the thin wa CLI client. Single-instance enforcement via flock, same-user-only auth via SO_PEERCRED, graceful shutdown, streaming notifications via subscribe. Transport layer only; use cases are a separate feature."

## Overview

This feature delivers the **transport layer** of the daemon: a JSON-RPC 2.0 server that listens on a per-user unix domain socket and dispatches incoming requests to a pluggable `Dispatcher` interface. The dispatcher is the seam at which the to-be-built use cases (feature 005) will plug in. No business logic lives here — this feature ships only the envelope parsing, method routing, subscription streaming, peer-credential gating, single-instance enforcement, and graceful lifecycle management.

Feature 003 delivered the secondary adapters (whatsmeow, sqlitestore, sqlitehistory). Feature 005 will deliver the use cases in `internal/app/`. Feature 006 will write `cmd/wad/main.go`, the composition root that wires everything together and satisfies the `Dispatcher` interface declared here. This feature is the first primary adapter in the hexagonal architecture and is the only primary adapter in scope for v0.1; future primary adapters (REST, MCP, Channels) will reuse the same underlying use cases.

The scope boundary is deliberately narrow: the dispatcher interface is a single method that receives a context, a method name, and raw JSON parameter bytes, and returns raw JSON result bytes or a typed error. Every socket-level concern (framing, auth, lifecycle, error mapping) is owned by this feature. Every business concern (what a `send` request actually does) is owned by feature 005. A fake dispatcher backs every contract test in this feature, keeping it fully decoupled from the rest of the codebase.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reliable request/response transport (Priority: P1)

As the daemon author building the `wa` CLI plugin, I need a request/response IPC channel with a well-known local socket path so that a thin client can invoke daemon capabilities (pair a device, send a message, list groups, query status) with a single short-lived TCP-like exchange — no Go linkage, no shared memory, no network exposure. The channel must speak a versioned, human-inspectable wire protocol that a future primary adapter can drop in next to, without breaking existing clients.

**Why this priority**: Without this, no client can talk to the daemon; the whole hexagonal architecture only becomes useful once a primary adapter routes real work through it. This is the MVP slice.

**Independent Test**: With a fake dispatcher that returns canned responses, a test client can open the socket, send a line-delimited JSON-RPC 2.0 request, receive the matching response, and close the connection. All framing, dispatch, and error-mapping code paths are exercised. Verifies envelope shape, method lookup, and success/error paths without any real use cases.

**Acceptance Scenarios**:

1. **Given** the server is listening and a fake dispatcher is wired to a method named `echo`, **When** a client sends `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"hello":"world"}}` followed by a newline, **Then** the client receives `{"jsonrpc":"2.0","id":1,"result":{"hello":"world"}}` followed by a newline, and the connection remains open.
2. **Given** the server is listening with no handler for method `nope`, **When** a client sends a valid request for `nope`, **Then** the client receives a response whose `error.code` is `-32601` (Method not found) and whose `id` matches the request.
3. **Given** the server is listening, **When** a client sends a byte sequence that is not valid JSON followed by a newline, **Then** the client receives an error response with `code: -32700` (Parse error) and the connection remains open so the client can retry.
4. **Given** the server is listening, **When** a client sends a JSON object that is missing the `method` field, **Then** the client receives an error response with `code: -32600` (Invalid Request).
5. **Given** a dispatcher that returns a domain error corresponding to an unknown recipient, **When** a request invokes that dispatcher, **Then** the client receives a JSON-RPC error response whose `code` is a stable server-defined code (e.g., `-32011`) and whose `data` field contains a machine-readable error identifier.

---

### User Story 2 - Same-user-only socket security (Priority: P1)

As the daemon author running on a multi-user macOS or Linux workstation, I need the guarantee that only processes owned by the same user that started the daemon can connect to the socket, so that another logged-in user on the same machine cannot read my WhatsApp messages or send messages as me. The protection must not rely on a shared secret or token — filesystem permissions plus kernel-level peer credential checks are the design.

**Why this priority**: This is the only authentication mechanism in the daemon. Without it, any other user on the box can hijack the session. Feature cannot ship without it.

**Independent Test**: A test creates the socket as uid A, then a second test process running as uid B attempts to connect. The connection is accepted at the listener but immediately closed after the peer-credential check. The second process observes an EOF on read before sending any bytes. No business logic is exercised; the rejection is purely at the transport layer.

**Acceptance Scenarios**:

1. **Given** the server is listening as uid 1000, **When** a process running as uid 1001 attempts to connect to the socket, **Then** the connection is closed before the server reads any bytes from it, and a structured log entry records the rejected peer uid.
2. **Given** the server is listening as uid 1000, **When** a process running as uid 1000 connects, **Then** the connection proceeds normally and the server begins reading JSON-RPC messages.
3. **Given** the server has just created the socket file, **When** the file is inspected on disk, **Then** its mode bits are exactly `0600` and its owner is the server's effective uid.

---

### User Story 3 - Streaming WhatsApp events to subscribers (Priority: P2)

As the daemon author, I need a way to push server-initiated events (incoming messages, delivery receipts, connection state changes) to clients that have opted in, so that the `wa wait` subcommand can block on a new message arriving and the future Claude Code plugin can treat the daemon as an event source. The mechanism must be a JSON-RPC 2.0 notification (no `id`) rather than a polling loop. Back-pressure must be enforced: a slow subscriber must not cause the server to grow memory without bound.

**Why this priority**: Essential for the plugin's inbound flow, but not required for the minimum "pair + send" end-to-end demo. P1 transport can be exercised without subscriptions.

**Independent Test**: A test client subscribes with a specific event-type filter, and a test-side harness injects events into the fake dispatcher's event channel. The client observes the matching notifications in order, the framing is valid, and each notification carries a `schema` string identifying the event version. A second test forces buffer overflow by pausing the client reader; the connection is closed with a specific error code after a bounded timeout.

**Acceptance Scenarios**:

1. **Given** a client that has sent `{"jsonrpc":"2.0","id":1,"method":"subscribe","params":{"events":["message"]}}` and received a success response, **When** the dispatcher emits a message event, **Then** the client receives `{"jsonrpc":"2.0","method":"event","params":{"schema":"wa.event/v1","type":"message",...}}` as a single line, with no `id` field.
2. **Given** a client that has subscribed only to `message` events, **When** a `receipt` event is emitted, **Then** the client does NOT receive it.
3. **Given** a client that has subscribed but has stopped reading, **When** the per-connection outbound buffer fills beyond its bound, **Then** the server closes the connection with a final error frame whose `code` is a documented backpressure code (e.g., `-32001`).
4. **Given** a client that has subscribed, **When** the client closes the connection cleanly, **Then** the server releases the subscription and stops delivering events to that subscription within 100ms.

---

### User Story 4 - Single-instance daemon guarantee (Priority: P1)

As the daemon author, I need the server to refuse to start if another daemon instance is already running, so that the whatsmeow SQLite session store — which cannot tolerate concurrent writers — is never corrupted. The check must be automatic (no manual lock files to clean up), must survive unclean kills (SIGKILL, power loss), and must produce an unmistakable error when it trips.

**Why this priority**: Corruption of the session store is a catastrophic, unrecoverable failure mode (device identity lost, forced re-pair, session-replaced events on the upstream WhatsApp server). P1 safety rail.

**Independent Test**: Start an in-process test server with a temp socket path. Start a second in-process test server with the same path. The second server's startup returns a specific error type within 500ms, and the first server continues running unaffected. Kill the first server ungracefully (simulated via panic in its goroutine). Restart a third server with the same path; it succeeds, proving stale-lock recovery works.

**Acceptance Scenarios**:

1. **Given** a daemon is already running and holding the socket lock, **When** a second daemon starts with the same socket path, **Then** the second daemon returns an "already running" error before creating a listener and exits with a distinguishable error code.
2. **Given** a previous daemon crashed and left a socket file behind but the lock is released, **When** a new daemon starts with the same socket path, **Then** the new daemon acquires the lock, unlinks the stale socket file, creates a new one, and listens successfully.
3. **Given** no prior daemon has run, **When** the daemon starts, **Then** it acquires the lock, creates the socket file with mode `0600`, and begins accepting connections within 200ms.

---

### User Story 5 - Graceful shutdown (Priority: P2)

As the operator (the user running `launchctl stop wa` or triggering a systemd user-unit stop), I need the daemon to drain in-flight requests, close client connections cleanly, unlink the socket file, and release the lock — all within a bounded timeout — so that a subsequent restart is immediate and no connection is left half-open on the client side. The shutdown path must be triggered both by OS signals (SIGTERM, SIGINT) and by an in-process `context.Context` cancellation.

**Why this priority**: Service managers (launchd/systemd) expect bounded shutdown. A hang past the shutdown deadline causes a forceful SIGKILL and the risk of the very corruption US4 prevents.

**Independent Test**: An in-process test server with a slow fake dispatcher (50ms latency) receives ten in-flight requests, then receives a shutdown signal. All ten responses arrive at the client before the socket closes. A new server on the same path starts successfully immediately after.

**Acceptance Scenarios**:

1. **Given** the server is handling in-flight requests, **When** shutdown is triggered, **Then** each in-flight request either completes and its response is delivered, or is cancelled via its own context with a documented error code, within the shutdown-deadline window.
2. **Given** the server has shut down cleanly, **When** the socket path is inspected, **Then** the socket file has been unlinked and the lock has been released.
3. **Given** a client is mid-way through reading a streamed event notification when shutdown begins, **When** shutdown progresses, **Then** the client receives a final error frame with a shutdown-specific error code before the connection is closed.

---

### Edge Cases

- **Socket path parent missing**: the server MUST create the parent directory (`$XDG_RUNTIME_DIR/wa/` or `~/Library/Caches/wa/`) with mode `0700` if it does not exist.
- **Parent directory wrong permissions**: the server MUST refuse to start if the parent directory exists but is world-writable or group-writable.
- **Symlink attack on socket path**: the server MUST refuse to use a socket path whose parent directory is a symlink it did not create, to prevent a symlink-swap exploit by another user.
- **Stale socket but live lock**: the lock file proves another instance is alive; the server MUST NOT unlink the socket file in this case.
- **Client sends partial line then closes**: the server MUST not block indefinitely waiting for the newline; it MUST detect EOF and close the per-connection goroutine.
- **Client sends a line larger than the maximum**: the server MUST reject any single framed message larger than 1 MiB with a Parse error, and MUST consume and discard the rest of the oversized message before resuming.
- **Batch requests**: the server MAY support JSON-RPC 2.0 batch syntax, but v0 scope does not require it; if a batch is received, the server returns an Invalid Request response for the batch envelope.
- **Dispatcher panic**: a panic inside a dispatcher invocation MUST be recovered, logged, and surfaced to the client as an Internal Error (`-32603`); the panic MUST NOT take down the whole server.
- **Orphaned subscription after server-side cancellation**: if the dispatcher signals shutdown of an event source mid-stream, the subscriber MUST receive a documented "source closed" notification and the subscription MUST be released.
- **Multiple subscriptions on one connection**: a single connection MAY hold multiple subscriptions (e.g., subscribe to `message` then later to `receipt`); the server MUST track them independently.
- **Socket path too long**: macOS and Linux limit `sun_path` to ~104 bytes; the server MUST validate the resolved path length at startup and fail with a clear error if exceeded.

## Requirements *(mandatory)*

### Functional Requirements

#### Transport and framing

- **FR-001**: The server MUST listen on a unix domain socket whose path is derived from the environment: on Linux, `$XDG_RUNTIME_DIR/wa/wa.sock`; on macOS, `~/Library/Caches/wa/wa.sock`.
- **FR-002**: The server MUST create any missing parent directory for the socket with mode `0700` and owner equal to the daemon's effective uid.
- **FR-003**: The socket file itself MUST have filesystem mode `0600` as observed on disk immediately after creation.
- **FR-004**: The server MUST speak JSON-RPC 2.0 per the official specification, with line-delimited framing: one complete JSON object per line terminated by a single `\n` byte, no leading whitespace, no embedded newlines inside the object.
- **FR-005**: The server MUST reject any single framed message larger than 1 MiB by returning a Parse error and discarding the oversized payload.
- **FR-006**: The server MUST accept standard JSON-RPC 2.0 request objects with fields `jsonrpc`, `method`, `params`, and optionally `id`. Requests without `id` are treated as notifications-from-client and MUST NOT receive a response.

#### Dispatch and method routing

- **FR-007**: The server MUST route each incoming request to a pluggable `Dispatcher` interface that takes a `context.Context`, a method name string, and the raw JSON-encoded `params`, and returns either raw JSON-encoded result bytes or a typed error.
- **FR-008**: Requests for methods not registered in the dispatcher MUST return JSON-RPC error code `-32601` (Method not found) with the offending method name echoed in `error.data`.
- **FR-009**: Request envelopes that are valid JSON but violate the JSON-RPC 2.0 schema (missing `jsonrpc`, missing `method`, wrong `jsonrpc` version, wrong `id` type, etc.) MUST return error code `-32600` (Invalid Request).
- **FR-010**: Request payloads that are not valid UTF-8 JSON MUST return error code `-32700` (Parse error) and MUST NOT close the connection.
- **FR-011**: Errors returned by the dispatcher MUST be translated to JSON-RPC error objects via a stable, documented error-code mapping table. Every domain error type named in feature 002 MUST have a reserved code in the table, even if the use case that raises it has not yet been written.
- **FR-012**: A panic inside a dispatcher invocation MUST be recovered, logged at ERROR level with stack trace, and surfaced to the client as JSON-RPC error code `-32603` (Internal Error); the panic MUST NOT terminate the accept loop or any other connection.

#### Peer authentication

- **FR-013**: On every accepted connection, the server MUST read the peer's uid using the kernel peer-credential mechanism (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` / `getpeereid` on macOS) before reading any bytes from the connection.
- **FR-014**: If the peer's uid differs from the daemon's effective uid, the server MUST close the connection immediately and MUST log the rejected peer uid at WARN level along with the connection's local timestamp.
- **FR-015**: The peer-credential check MUST complete within 50 ms of accept under normal conditions; a slow or unresponsive peer-credential call MUST not block the accept loop for longer than 1 second before the connection is forcibly dropped.

#### Single-instance enforcement

- **FR-016**: Before creating the listener, the server MUST acquire an exclusive, non-blocking advisory file lock on the socket path (or a sibling lock file with suffix `.lock`) using the same mechanism used by the sqlitestore and sqlitehistory adapters in feature 003.
- **FR-017**: If the lock cannot be acquired because another process holds it, the server MUST return a distinguishable "already running" error within 500 ms of attempting the lock and MUST NOT create or unlink the socket file.
- **FR-018**: If the lock is successfully acquired and a stale socket file exists at the target path, the server MUST unlink the stale file and proceed; this is safe because the lock proves no other daemon is holding it.
- **FR-019**: The lock MUST be released when the server exits, either via graceful shutdown or by the kernel on process termination.

#### Streaming and subscriptions

- **FR-020**: The server MUST expose a reserved method named `subscribe` whose `params` object contains at minimum an `events` field listing event type names the client wishes to receive.
- **FR-021**: A successful `subscribe` call MUST return a result object containing a `subscriptionId` string and a schema version string of the form `wa.event/v<MAJOR>`.
- **FR-022**: After a successful subscription, the server MUST forward events from the dispatcher's event source to the subscribing connection as JSON-RPC 2.0 notifications (no `id` field) with method name `"event"` and a `params` object containing at minimum `schema`, `type`, and the event payload.
- **FR-023**: The server MUST filter events per-subscription according to the `events` list; events whose type is not listed MUST NOT be delivered to that subscription.
- **FR-024**: Each connection MUST have a bounded outbound notification buffer of at least 1024 events; when the buffer fills, the server MUST close the connection with a final error frame whose `code` is a documented backpressure code (reserved: `-32001`).
- **FR-025**: The server MUST release all subscriptions associated with a connection within 100 ms of the connection closing, without leaking goroutines.
- **FR-026**: A reserved method named `unsubscribe` MUST accept a `subscriptionId` and release the corresponding subscription while keeping the connection open.

#### Concurrency and resource limits

- **FR-027**: The server MUST accept and serve at least 16 concurrent connections without errors under normal load.
- **FR-028**: Each connection MUST permit pipelining of up to 32 in-flight requests before the server applies back-pressure by refusing to read further bytes from that connection.
- **FR-029**: The server MUST bound total goroutines per connection at a documented constant (reader, writer, and one per in-flight dispatch, capped by FR-028).
- **FR-030**: The server MUST not leak goroutines after a connection closes; verified by a goroutine-count assertion in the contract test suite.

#### Lifecycle and shutdown

- **FR-031**: The server's run loop MUST accept a `context.Context` and MUST begin graceful shutdown the moment that context is cancelled.
- **FR-032**: Graceful shutdown MUST drain in-flight requests up to a configurable deadline (default: 5 seconds); requests that do not complete within the deadline MUST have their per-request context cancelled and a best-effort error response sent.
- **FR-033**: During graceful shutdown, the server MUST send a final error notification with a documented shutdown code to every active subscription before closing its connection.
- **FR-034**: After graceful shutdown completes, the server MUST unlink the socket file and release the lock; a subsequent startup MUST succeed immediately.
- **FR-035**: The server MUST expose a blocking `Wait()` method that returns when shutdown is fully complete, suitable for use by a `cmd/wad/main.go` that exits only when the server has finished.

#### Observability

- **FR-036**: The server MUST emit structured logs via `log/slog` for at least: listener start, listener stop, connection accepted, peer-credential accept, peer-credential reject, method dispatched (at DEBUG), dispatch error (at ERROR), subscription created, subscription closed, backpressure close, graceful shutdown initiated, graceful shutdown complete.
- **FR-037**: Every log entry originating from a connection MUST include a connection-scoped identifier (monotonic integer per listener lifetime) so that logs can be correlated across the connection's lifetime.
- **FR-038**: The server MUST NOT log the contents of request `params` or response `result` at any level, to prevent accidental leakage of message bodies into the log file.

#### Documentation and stability

- **FR-039**: The v0 wire protocol MUST be documented in `specs/004-socket-adapter/contracts/wire-protocol.md`, enumerating every method name the socket adapter reserves (`subscribe`, `unsubscribe`), every JSON-RPC error code the server uses, and the envelope schema.
- **FR-040**: The documented wire protocol MUST NOT make normative claims about business methods (`send`, `pair`, `status`, etc.) because those are defined by feature 005; the contract MUST list them as "reserved for later features" if at all.
- **FR-041**: The `Dispatcher` interface contract MUST be documented in `specs/004-socket-adapter/contracts/dispatcher.md` with the exact shape, the context semantics, the error return conventions, and the event-source semantics.
- **FR-042**: Every functional requirement in this feature MUST be covered by at least one test in the contract test suite at `internal/adapters/primary/socket/sockettest/`, exercised against a fake dispatcher.

### Key Entities

- **Wire Request** — a JSON object containing `jsonrpc: "2.0"`, an optional `id` (string or number), a `method` (string), and an optional `params` (object or array).
- **Wire Response** — a JSON object containing `jsonrpc: "2.0"`, the matching `id`, and exactly one of `result` or `error`.
- **Wire Notification (server to client)** — a JSON object containing `jsonrpc: "2.0"`, `method: "event"`, and a `params` object with a `schema` string and a `type` string identifying the event.
- **Wire Error** — an object with `code` (integer), `message` (string), and optional `data` (any JSON value); the code maps to either a JSON-RPC-standard value or a server-reserved value in the feature's error code table.
- **Dispatcher** — the pluggable seam between the socket adapter and the use cases. Has two responsibilities: handle a request (`Handle(ctx, method, params) (result, error)`) and expose an event source (`Events() <-chan Event`). Every test in this feature uses a fake dispatcher; the real dispatcher is written in feature 005 or 006.
- **Subscription** — per-connection state recording which event types the client has opted into, the subscription identifier string, and the outbound buffer.
- **Connection State** — per-connection bookkeeping: peer uid (after peer-cred check), connection id, in-flight request count, active subscriptions, last activity timestamp.
- **Error Code Table** — a documented mapping from domain error classes (declared in feature 002) and transport error classes (declared here) to stable JSON-RPC error code integers. The table is append-only: existing codes MUST NOT be renumbered.
- **Socket Path** — an absolute filesystem path derived at server start from the OS and environment; validated for length, parent permissions, and symlink safety.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A local request/response roundtrip against a fake dispatcher completes in under 10 ms on a developer laptop, measured from client write to client read.
- **SC-002**: A connection from a different uid is rejected within 50 ms of accept, before any bytes are read from the connection, measured by wall-clock timing in the contract test.
- **SC-003**: A second daemon attempting to start with the same socket path exits with a distinguishable "already running" error in under 500 ms, without disturbing the first daemon's state.
- **SC-004**: The server sustains 1000 sequential request/response cycles on a single connection without resident memory growth exceeding 10 MiB RSS, measured by a benchmark in the contract suite.
- **SC-005**: Graceful shutdown completes within 2 seconds of context cancellation when there are no in-flight requests, and within 6 seconds (5-second drain + 1-second margin) when there are in-flight requests with a slow dispatcher.
- **SC-006**: A subscriber whose reader has stalled is closed with a backpressure error within 1 second after its outbound buffer is full, and the server's goroutine count returns to baseline within 1 second after that close.
- **SC-007**: 100% of v0 JSON-RPC methods that the socket adapter itself reserves (`subscribe`, `unsubscribe`) are listed in `contracts/wire-protocol.md` before any code is written, and every error code the server emits is listed in the error code table.
- **SC-008**: `go test -race ./internal/adapters/primary/socket/...` passes with zero race warnings and zero leaked goroutines (verified with a goroutine-count assertion).
- **SC-009**: The contract test suite runs in under 10 seconds wall clock on a developer laptop, end to end.
- **SC-010**: The depguard rule forbidding `whatsmeow` imports from `internal/domain` and `internal/app` continues to pass; the socket adapter MUST NOT introduce any whatsmeow dependency of its own.

## Assumptions

- The daemon is a single-user, single-instance process. Same-user-only authentication via peer credentials plus `0600` socket permissions is sufficient for the v0.1 threat model. Multi-user, multi-tenant, or cross-machine access are all out of scope.
- The CLI client is trusted (it ships with the daemon in the same release). Adversarial input is tolerated (parse errors, oversized messages, malformed envelopes) but the threat model does not include a malicious client with legitimate credentials attempting to exploit the daemon.
- Dispatcher implementations run on a per-request goroutine spawned by the socket adapter; they are responsible for honoring their own `context.Context` for cancellation.
- Graceful shutdown is best-effort: on `SIGKILL` or power loss, the socket file will be left behind, but the flock-based startup check will unlink it and proceed on the next launch.
- TLS is explicitly not required. The transport is a local unix domain socket.
- Batch JSON-RPC requests are not required in v0. They may be added later without a breaking schema change by extending the wire-protocol document.
- The `Dispatcher` interface is implemented in a later feature. Until then, a fake dispatcher in the contract test suite stands in for the real one.
- The JSON-RPC error code space used by this server is `-32099..-32000` (the JSON-RPC-reserved "server error" block); no attempt is made to coexist with a pre-existing external JSON-RPC vocabulary.
- The event source (`Dispatcher.Events() <-chan Event`) is owned and closed by the dispatcher; the socket adapter only reads from it.
- Existing domain types from feature 002 (`JID`, `Action`, `AuditEvent`, and the error sentinels) are available for import into the error-mapping table; no domain types are changed by this feature.
- Existing file-lock primitives from feature 003 (`rogpeppe/go-internal/lockedfile`) are reused here; no new locking library is introduced.

## Dependencies

- **Feature 002 (domain-and-ports)** — for the domain error sentinels that are mapped into the error code table. No changes to those types are made here.
- **Feature 003 (whatsmeow-adapter)** — only for reuse of the `lockedfile` dependency and the sysexits-style error pattern. No direct import of any whatsmeow package. The depguard rule that forbids whatsmeow imports from the hexagonal core continues to apply.
- **Feature 005 (application use cases)** — will implement the `Dispatcher` interface declared here. Not a blocker for this feature: the fake dispatcher in the contract suite is sufficient to ship 004 on its own.
- **Feature 006 (cmd/wad composition root)** — will instantiate the socket server with a real dispatcher and a real lifecycle. Not a blocker for this feature.

## Out of Scope

- The business methods (`send`, `sendMedia`, `pair`, `status`, `groups`, `markRead`, `wait`) — reserved for feature 005.
- The `cmd/wad/main.go` composition root — reserved for feature 006.
- The `cmd/wa` CLI client — reserved for feature 006.
- launchd / systemd unit files — reserved for feature 007.
- REST, MCP, Channels, or any other primary adapter — deferred to after v0.1.
- Authentication via tokens or shared secrets — out of scope by design.
- TLS termination, client certificate verification, or mutual TLS — not applicable to a local unix socket.
- Batch JSON-RPC requests — deferred; may be added later without a breaking change.
- Server-side rate limiting — the rate limiter lives in the use case layer (feature 005), not the transport layer.
- Metrics / Prometheus endpoint — deferred; the socket adapter only emits structured logs in v0.
