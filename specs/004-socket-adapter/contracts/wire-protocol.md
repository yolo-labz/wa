# Contract: Wire Protocol

**Feature**: 004-socket-adapter
**Version**: `wa.rpc/v1`

This document is the authoritative wire-level contract for every byte that flows over the `wa` daemon's unix domain socket. Any change to a field name, type, or error code in this document is a **breaking** change that requires bumping the schema version string from `v1` to `v2` and retaining the old parser for one release.

The rules in this contract are binding on:
- Feature 004 (this feature) for implementation
- Feature 005 for any new methods it adds — codes must come from the reserved range `-32011..-32020`, and new methods must be documented here
- Feature 006 for the `cmd/wa` client implementation
- Any future primary adapter that wants to interoperate with existing clients

## Transport

- **Socket family**: `AF_UNIX` (unix domain socket), `SOCK_STREAM`
- **Socket path**:
  - Linux: `$XDG_RUNTIME_DIR/wa/wa.sock`
  - macOS: `~/Library/Caches/wa/wa.sock`
- **File mode**: `0600` on the socket, `0700` on the parent directory
- **Ownership**: the daemon's effective uid
- **Authentication**: kernel-enforced peer credential check; peer uid MUST equal server effective uid
- **Single-instance lock**: sibling file `<socket>.lock` held exclusively via `flock()`
- **Encoding**: UTF-8 JSON
- **Framing**: line-delimited — exactly one JSON object per line terminated by a single `\n` byte. No leading whitespace, no embedded newlines inside the object, no trailing whitespace.
- **Message size cap**: 1 MiB (1,048,576 bytes) per framed line. Oversize triggers `-32004 OversizedMessage` and connection close.
- **Character set**: JSON strings MUST be valid UTF-8. Invalid byte sequences trigger `-32700 Parse error`.

## Envelope types

All four envelope shapes are JSON-RPC 2.0 §4/§5 compliant.

### 1. Request

A client-originated call that expects a response.

```json
{"jsonrpc":"2.0","id":<Number|String>,"method":<String>,"params":<Object|Array>}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `jsonrpc` | string | yes | MUST be the literal `"2.0"` |
| `id` | number or string | yes | Any non-null; the response will echo this verbatim |
| `method` | string | yes | Case-sensitive method name |
| `params` | object or array | no | Omitted when the method takes no arguments; scalar values are rejected |

### 2. Client notification

A client-originated call with no response expected.

```json
{"jsonrpc":"2.0","method":<String>,"params":<Object|Array>}
```

Distinguished from a Request by the absence of the `id` field. Per JSON-RPC 2.0 §4.1, the server MUST NOT emit a response for a client notification, even on error. Parse errors on a client notification are logged but not reported on the wire.

### 3. Success response

```json
{"jsonrpc":"2.0","id":<echo of request id>,"result":<Any>}
```

The `result` field may be any valid JSON value. A method that conceptually returns nothing MUST return `result: null` (not omit the field, not return an empty object).

### 4. Error response

```json
{"jsonrpc":"2.0","id":<echo of request id|null>,"error":{"code":<Integer>,"message":<String>,"data":<Any>}}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | integer | yes | From the error code table below |
| `message` | string | yes | Short, human-readable |
| `data` | any | no | Machine-readable details; see per-code tables below |

`id` is `null` only when the server could not parse the request enough to determine it (e.g., `-32700 Parse error`, `-32600 Invalid Request` where the id was malformed).

### 5. Server notification (server-initiated push)

```json
{"jsonrpc":"2.0","method":"event","params":{"schema":<String>,"type":<String>,"subscriptionId":<String>,...}}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `method` | string | yes | MUST be the literal `"event"` |
| `params.schema` | string | yes | Format: `wa.event/v<MAJOR>`; currently `wa.event/v1` |
| `params.type` | string | yes | Event type name; subscription filters match on this |
| `params.subscriptionId` | string | yes | Echoes the id returned from `subscribe` |
| `params.*` | any | — | Additional event payload fields (defined by feature 005 per event type) |

Server notifications have no `id` — they are fire-and-forget. The client MUST tolerate server notifications arriving interleaved with responses.

## Error code table

### Standard JSON-RPC 2.0 codes (emitted by the socket adapter)

| Code | Name | Meaning | `data` contents |
|---|---|---|---|
| `-32700` | Parse error | Not valid JSON, or invalid UTF-8, or framed message oversized | `{ "hint": <string describing which check failed> }` |
| `-32600` | Invalid Request | Parsed as JSON but violates JSON-RPC 2.0 schema | `{ "hint": <string> }` |
| `-32601` | Method not found | Method name is not registered in the dispatcher | `{ "method": <offending method name> }` |
| `-32602` | Invalid params | Dispatcher rejected the params as malformed | `{ "hint": <string> }` |
| `-32603` | Internal error | Dispatcher panicked; the panic was recovered and logged | `{ "hint": "internal error" }` — stack trace is NEVER sent on the wire |

### Server-defined codes (socket adapter, feature 004)

| Code | Name | Meaning | `data` contents |
|---|---|---|---|
| `-32000` | PeerCredRejected | Peer uid at accept time did not equal server uid | `{ "expected": <uid>, "actual": <uid> }` |
| `-32001` | Backpressure | Outbound mailbox full; subscription-bearing connection is about to be closed | `{ "subscriptionId": <string> }` |
| `-32002` | ShutdownInProgress | New request arrived after shutdown was initiated | `{}` |
| `-32003` | RequestTimeoutDuringShutdown | An in-flight request was cancelled because the drain deadline elapsed | `{ "deadlineSeconds": <number> }` |
| `-32004` | OversizedMessage | A single framed message exceeded the 1 MiB cap | `{ "maxBytes": 1048576, "receivedBytes": <int> }` |
| `-32005` | SubscriptionClosed | The dispatcher's event source closed mid-stream | `{ "subscriptionId": <string> }` |
| `-32006..-32010` | reserved | — | — |

### Reserved for feature 005 (domain errors)

| Code | Name | Meaning |
|---|---|---|
| `-32011` | NotPaired | No device is paired; pair first |
| `-32012` | NotAllowlisted | Target JID is not on the allowlist for this action |
| `-32013` | RateLimited | Per-second/per-minute/per-day rate limit exceeded |
| `-32014` | WarmupActive | Warmup period rejects this call at the current throttle level |
| `-32015` | InvalidJID | JID string failed validation |
| `-32016` | MessageTooLarge | Message body exceeds the 64 KiB text or 16 MiB media cap |
| `-32017` | SessionInvalid | Session store is unreadable or corrupt |
| `-32018` | Disconnected | WhatsApp upstream is not currently connected |
| `-32019..-32020` | reserved | — |

Feature 004 MUST NOT emit any code in the `-32011..-32020` range. Feature 004's contract tests MUST verify this by scanning the source for error-code literals.

### Reserved ranges

- `-32021..-32099` — reserved for future server-defined codes (later features)
- `-32768..-32100` — reserved by the JSON-RPC 2.0 spec, MUST NOT be used
- codes ≥ 0 — not valid per JSON-RPC 2.0; MUST NOT be emitted

## Methods reserved by the socket adapter

Feature 004 reserves two method names. Any dispatcher implementation MUST NOT register a handler for these names; the socket adapter intercepts them before dispatch.

### `subscribe`

**Request**:
```json
{"jsonrpc":"2.0","id":1,"method":"subscribe","params":{"events":["message","receipt"]}}
```

**Params**:
| Field | Type | Required | Notes |
|---|---|---|---|
| `events` | array of strings | yes | Event type names to opt into; empty array subscribes to nothing (valid but useless) |

**Success response**:
```json
{"jsonrpc":"2.0","id":1,"result":{"subscriptionId":"<uuid>","schema":"wa.event/v1"}}
```

**Errors**:
- `-32602 Invalid params` if `events` is missing or not an array of strings
- `-32603 Internal error` if subscription creation fails for reasons other than client input

### `unsubscribe`

**Request**:
```json
{"jsonrpc":"2.0","id":2,"method":"unsubscribe","params":{"subscriptionId":"<uuid>"}}
```

**Params**:
| Field | Type | Required | Notes |
|---|---|---|---|
| `subscriptionId` | string | yes | Must be an id returned from a prior `subscribe` call on the same connection |

**Success response**:
```json
{"jsonrpc":"2.0","id":2,"result":null}
```

**Errors**:
- `-32602 Invalid params` if `subscriptionId` is missing or not a string
- `-32602 Invalid params` if the id is not found in this connection's subscription table (subscriptions do not cross connections)

## Methods reserved for feature 005

Feature 005 will add use-case methods to the dispatcher. The following names are reserved; feature 004 MUST NOT register handlers for them but MUST NOT treat them as special either — the dispatcher will return `-32601 Method not found` until feature 005 implements them.

| Method | Purpose | Feature |
|---|---|---|
| `send` | Send a text or media message to a JID | 005 |
| `sendMedia` | Send a media attachment | 005 |
| `markRead` | Mark a message as read | 005 |
| `pair` | Initiate device pairing | 005 |
| `status` | Report daemon and connection state | 005 |
| `groups` | List joined groups | 005 |
| `wait` | Block until an event of a given type arrives (syntactic sugar over `subscribe`) | 005 |

These names are listed here so that feature 004's documentation is complete and feature 005's contract has a seat reserved. Feature 004 does not implement any of them.

## Schema versioning

The wire protocol version is a pair: the RPC envelope version and the event schema version.

- **RPC envelope version**: currently `wa.rpc/v1`. Advertised implicitly by the socket path (`wa.sock`) and the first response. The server does not currently enforce a client-declared version — JSON-RPC 2.0 is assumed.
- **Event schema version**: declared in every server notification via `params.schema`. Currently `wa.event/v1`. A major-version bump (`v2`) is a breaking change; clients MUST ignore events whose major version they do not recognize.

A future feature may add a `hello` method that negotiates versions explicitly. Until then, major-version-compatible evolution is the rule.

## Test coverage requirement

Every entry in this document — every error code, every envelope shape, every reserved method, every invariant — MUST be exercised by at least one contract test in `internal/adapters/primary/socket/sockettest/`. Feature 004 cannot merge until CI confirms all rows in this document have a corresponding test.
