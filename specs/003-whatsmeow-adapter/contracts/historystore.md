# Contract: `HistoryStore` port (the 8th port)

**Applies to**: `internal/app/ports.go` and any adapter implementing `app.HistoryStore`
**Enforced by**: spec FR-019, FR-020, FR-022; the contract test suite at `internal/app/porttest/historystore.go`.

This file is the **canonical Go signature + behavioural contract** for the new 8th port. It is the literal source of the `HistoryStore` block in `internal/app/ports.go`.

## Signature

```go
// HistoryStore is the secondary port for historical message lookup. Added by
// feature 003 per CLAUDE.md §"Reliability principles" rule 20 (Cockburn:
// ports as intent of conversation, no fixed port count).
type HistoryStore interface {
    LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
}
```

## Parameters

| Param | Meaning | Constraints |
|---|---|---|
| `ctx` | Cancellation + deadline | First parameter; standard Go convention; on `Done()`, return `ctx.Err()` |
| `chat` | The chat JID to fetch messages from (user or group) | MUST satisfy `chat.IsUser() \|\| chat.IsGroup()`; otherwise return `domain.ErrInvalidJID` wrapped |
| `before` | Cursor — return messages strictly older than this ID. Zero value means "start from newest". | Opaque to the port; the implementation looks it up in its own store |
| `limit` | Maximum number of messages to return | MUST be > 0; values > 200 are clamped to 200 (mautrix-observed phone cap is 50/round-trip but a single LoadMore call may aggregate multiple round-trips) |

## Returns

| Return | Meaning |
|---|---|
| `[]domain.Message` | Up to `limit` messages, ordered by timestamp **descending** (newest first). Empty slice + nil error means "no more messages exist for this chat" — NOT a typed error. |
| `error` | Wrapped sentinel if invalid input or infrastructure failure; nil on success |

## Behavioural contract (test clause IDs `HS1`–`HS6`)

| # | Precondition | Action | Postcondition |
|---|---|---|---|
| HS1 | Local store has 5 messages in chat C, all newer than `before=zero`; `limit=10` | `LoadMore(ctx, C, 0, 10)` | Returns 5 messages in descending timestamp order, nil error. No remote round-trip. |
| HS2 | Local store has 0 messages in chat C; the implementation supports remote backfill (whatsmeow); `limit=10` | `LoadMore(ctx, C, 0, 10)` | Issues a remote backfill request, waits up to 30s, returns up to 10 messages from the response, nil error. May return < 10 if the phone has fewer messages available. |
| HS3 | Local store has 0 messages in chat C; the implementation does NOT support remote backfill (in-memory adapter) | `LoadMore(ctx, C, 0, 10)` | Returns empty slice, nil error. NOT an error — empty is the success case for "you have everything that exists locally". |
| HS4 | `chat` has zero JID | `LoadMore(ctx, JID{}, 0, 10)` | Returns nil slice, error wrapping `domain.ErrInvalidJID`. No I/O performed. |
| HS5 | `limit <= 0` | `LoadMore(ctx, C, 0, 0)` or `LoadMore(ctx, C, 0, -1)` | Returns nil slice, error wrapping a typed "invalid limit" indicator. No I/O performed. |
| HS6 | ctx already cancelled | `LoadMore(canceled, C, 0, 10)` | Returns nil slice, `context.Canceled`. No I/O performed (early return on `ctx.Err() != nil`). |

## Concurrency contract

- `LoadMore` MUST be safe for concurrent calls from multiple goroutines.
- Calls for different `chat` JIDs MUST proceed in parallel without serialisation.
- Calls for the same `chat` JID MAY serialise inside the implementation if the underlying remote source has per-chat ordering constraints.

## Error categories

- `domain.ErrInvalidJID` (wrapped) — input validation failure (HS4)
- A typed "invalid limit" sentinel (e.g., `errors.New("invalid limit")` wrapped) — HS5
- `context.Canceled` / `context.DeadlineExceeded` — caller cancellation (HS6) or remote timeout
- A typed infrastructure error (network, sqlite, decompression) for remote/local I/O failures
- Never `domain.ErrDisconnected` from `LoadMore` — disconnection during a remote backfill returns `context.DeadlineExceeded` after 30s, NOT `ErrDisconnected`. The `ErrDisconnected` sentinel is reserved for `MessageSender.Send`.

## In-memory adapter satisfaction

The in-memory adapter from feature 002 (`internal/adapters/secondary/memory/`) MUST be extended to satisfy `HistoryStore` by storing inserted messages in a slice keyed by chat JID. Its implementation always returns from local storage; HS2 (remote backfill) is not exercised by the in-memory adapter — that's HS3's role. The contract test suite skips HS2 when the factory's adapter does not declare `SupportsRemoteBackfill() bool` returning true (or equivalent capability check).

## whatsmeow adapter satisfaction

The whatsmeow adapter (`internal/adapters/secondary/whatsmeow/`) implements `LoadMore` by:

1. Querying `sqlitehistory.Store.LoadMore` first
2. If the local query returns fewer than `limit` rows, calling `client.BuildHistorySyncRequest(lastInfo, limit-localCount)` (capped at 50 per round-trip)
3. Sending the peer message via `client.SendMessage(ctx, ownJID.ToNonAD(), msg, SendRequestExtra{Peer: true})`
4. Registering a request-ID-keyed channel in `historyReqs sync.Map` (per research §D1)
5. `select`ing on the channel and a 30-second `time.NewTimer`
6. On response, persisting the new messages via `sqlitehistory.Store.Insert` and returning the merged result (newest-first)
7. On timeout, returning `context.DeadlineExceeded` and leaving the local store untouched

## Forbidden patterns

- `LoadMore` MUST NOT return `interface{}` or `any` — only `[]domain.Message`
- `LoadMore` MUST NOT mutate `before` or `chat` (Go pass-by-value makes this structurally safe; flag if a pointer receiver creeps in)
- `LoadMore` MUST NOT log to stdout/stderr (use the audit log via the `AuditLog` port if the call needs recording)
- `LoadMore` MUST NOT call into `whatsmeow` from any non-whatsmeow adapter (the depguard rule already prevents this)
