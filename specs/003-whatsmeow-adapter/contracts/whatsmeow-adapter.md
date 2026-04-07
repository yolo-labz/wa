# Contract: whatsmeow Secondary Adapter

**Applies to**: `internal/adapters/secondary/whatsmeow/*.go`
**Enforced by**: spec FR-001..FR-017, FR-021, FR-022; the contract test suite at `internal/app/porttest/`.

This file is the **construction, lifecycle, translation, and behavioural contract** for the whatsmeow adapter. It complements `historystore.md` (which specifies only the new 8th port).

## Universal rules

1. **Zero whatsmeow types escape this package.** No exported function, method, struct field, or return value may carry a `whatsmeow.*` or `whatsmeow/types.*` type. The `core-no-whatsmeow` `depguard` rule blocks the inverse direction; this rule is the dual.
2. **Single-package import isolation.** `go.mau.fi/whatsmeow` and its subpackages may be imported ONLY from files under `internal/adapters/secondary/whatsmeow/`. The `sqlitestore/` and `sqlitehistory/` siblings have their own narrow imports (`whatsmeow/sqlstore` and `modernc.org/sqlite` respectively).
3. **`clientCtx` is daemon-scoped.** Constructed once in `Open` from `context.Background()`, cancelled only in `Close`. Per-request contexts are passed only to in-process operations (e.g. waiting on the `historyReqs` channel), never to the underlying `*whatsmeow.Client`.
4. **`whatsmeowClient` interface is the test boundary.** Production code uses `*whatsmeow.Client`; tests use a hand-rolled fake (research §D4). Adding a new `*whatsmeow.Client` method to the adapter MUST extend the interface in the same commit.

## Construction — `Open`

```go
func Open(parentCtx context.Context, sessionStorePath, historyStorePath string, allowlist *domain.Allowlist) (*Adapter, error)
```

**Sequence** (per `data-model.md` §"Construction order"):

1. `os.MkdirAll(filepath.Dir(sessionStorePath), 0o700)` — XDG data dir
2. `sqlitestore.Open(ctx, sessionStorePath)` — opens whatsmeow ratchet store, acquires `flock` on `session.db`
3. `sqlitehistory.Open(ctx, historyStorePath)` — opens our history store, acquires SECOND `flock` on `messages.db`
4. Read or create `*store.Device` from `sqlitestore.Container`
5. Mutate `device.DeviceProps.HistorySyncConfig = &waCompanionReg.DeviceProps_HistorySyncConfig{FullSyncDaysLimit: proto.Uint32(7), FullSyncSizeMbLimit: proto.Uint32(20), StorageQuotaMb: proto.Uint32(100)}` — bounds the source per FR-019
6. Construct `client := whatsmeow.NewClient(device, waLog)` and apply the 12 production flags (FR-009)
7. Set `client.ManualHistorySyncDownload = true`
8. Register event handler: `client.AddEventHandlerWithSuccessStatus(adapter.handleWAEvent)`
9. Create `clientCtx, clientCancel := context.WithCancel(context.Background())` — NOT `parentCtx`
10. Return `*Adapter`

**Error handling**: any failure unwinds in reverse order — close history store, close session store, return wrapped error. The two flocks are released by their respective `Close()` calls.

**Concurrency**: `Open` is NOT safe to call concurrently with itself for the same paths. Two simultaneous `Open` calls against the same `sessionStorePath` will both acquire the in-process mutex but only one will succeed at the OS-level `flock`; the other returns `domain.ErrInvalidJID` wrapped (or a new "session locked" sentinel). Use a single `Open` per process.

## JID translator (`translate_jid.go`)

```go
// toDomain translates a whatsmeow types.JID into a domain.JID via canonical
// string round-trip. Lossless: domain.Parse(jid.String()) == jid for every
// valid jid.
func toDomain(j waTypes.JID) (domain.JID, error) {
    return domain.Parse(j.String())
}

// toWhatsmeow translates a domain.JID into a whatsmeow types.JID via the
// same canonical string. Panics if the input is the zero domain.JID — that
// indicates a programmer error upstream.
func toWhatsmeow(j domain.JID) waTypes.JID {
    if j.IsZero() {
        panic("whatsmeow adapter: toWhatsmeow called with zero JID")
    }
    parsed, err := waTypes.ParseJID(j.String())
    if err != nil {
        panic(fmt.Sprintf("whatsmeow adapter: domain.JID %q failed waTypes.ParseJID: %v", j.String(), err))
    }
    return parsed
}
```

The panic on zero JID is justified because a zero JID reaching this function is a contract violation by the caller — every domain function that produces a JID either returns a non-zero value or returns an error. The panic surfaces the bug at the offending call site instead of silently corrupting a downstream message.

## Event translator (`translate_event.go`)

`handleWAEvent` is the central switch fed by `client.AddEventHandlerWithSuccessStatus`. It runs on whatsmeow's event goroutine; the body MUST be fast (no blocking I/O on the hot path).

```go
func (a *Adapter) handleWAEvent(rawEvt any) {
    defer a.recoverPanic("handleWAEvent")

    seq := domain.EventID(strconv.FormatUint(a.eventSeq.Add(1), 10))
    var translated domain.Event

    switch evt := rawEvt.(type) {
    case *events.Message:        translated = translateMessage(seq, evt)
    case *events.Receipt:        translated = translateReceipt(seq, evt)
    case *events.Connected:      translated = translateConnected(seq, evt)
    case *events.Disconnected:   translated = translateDisconnected(seq, evt)
    case *events.LoggedOut:      a.handleLoggedOut(seq); return
    case *events.PairSuccess:    translated = translatePairSuccess(seq, evt)
    case *events.PairError:      translated = translatePairError(seq, evt)
    case *events.QR:             return  // QR is handled by the GetQRChannel flow in pair.go, not here
    case *events.HistorySyncNotification:
        a.handleHistorySync(seq, evt)
        return
    default:
        return  // unknown event type; log to audit but do not surface
    }

    select {
    case a.eventCh <- translated:
    case <-a.clientCtx.Done():
    default:
        // bounded channel full — drop with audit entry; do NOT block whatsmeow's hot path
        a.recordAudit(domain.AuditAction(0), translated, "eventCh full, dropped")
    }
}
```

**Translation rules per event type**:

| whatsmeow event | domain.Event variant | Notes |
|---|---|---|
| `*events.Message` | `MessageEvent{ID, TS, From, Message: TextMessage/MediaMessage/ReactionMessage}` | Variant chosen by `evt.Message` content; media path is the whatsmeow encrypted-media URL stored opaquely |
| `*events.Receipt` | `ReceiptEvent{ID, TS, Chat, MessageID, Status}` | `Status` mapped from `evt.Type` (`Read`, `Delivered`, `Played`) |
| `*events.Connected` | `ConnectionEvent{ID, TS, State: ConnConnected}` | |
| `*events.Disconnected` | `ConnectionEvent{ID, TS, State: ConnDisconnected}` | |
| `*events.LoggedOut` | `PairingEvent{ID, TS, State: PairFailure, Code: ""}` + `SessionStore.Clear()` | Per FR-010; the adapter refuses subsequent `Send` until a fresh `Pair` |
| `*events.PairSuccess` | `PairingEvent{ID, TS, State: PairSuccess, Code: ""}` | |
| `*events.PairError` | `PairingEvent{ID, TS, State: PairFailure, Code: ""}` | `evt.Error` recorded in audit log only |
| `*events.QR` | (no domain event) | The QR string is delivered via `GetQRChannel`, not via the event handler |
| `*events.HistorySyncNotification` | (no domain event in `eventCh`) | Routed to `handleHistorySync`, which downloads the blob, persists into `sqlitehistory.Store`, and (for `SyncType == ON_DEMAND`) forwards to the matching `historyReqs` channel |

## Send (`send.go`)

```go
func (a *Adapter) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
    if a.closed.Load() {
        return "", fmt.Errorf("whatsmeow.Send: %w", domain.ErrDisconnected)
    }
    if !a.client.IsConnected() {
        return "", fmt.Errorf("whatsmeow.Send: %w", domain.ErrDisconnected)
    }
    if err := msg.Validate(); err != nil {
        return "", fmt.Errorf("whatsmeow.Send: %w", err)
    }

    waMsg, to, err := translateOutbound(msg)
    if err != nil {
        return "", fmt.Errorf("whatsmeow.Send: %w", err)
    }

    resp, err := a.client.SendMessage(a.clientCtx, to, waMsg)  // NOT ctx — clientCtx is daemon-scoped
    if err != nil {
        return "", fmt.Errorf("whatsmeow.Send: %w", err)
    }
    return domain.MessageID(resp.ID), nil
}
```

**Notes**:

- The validation happens BEFORE `translateOutbound` — fail fast on invalid input
- `a.clientCtx` is passed to `SendMessage`, NOT the caller's `ctx`. Per FR-012, the whatsmeow client lifetime must be daemon-scoped. The caller's `ctx` only controls how long the adapter waits inside its own code.
- `domain.ErrDisconnected` is returned eagerly when `closed=true` OR `IsConnected=false`. No queueing.

## EventStream (`stream.go`)

```go
func (a *Adapter) Next(ctx context.Context) (domain.Event, error) {
    select {
    case evt := <-a.eventCh:
        return evt, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-a.clientCtx.Done():
        return nil, fmt.Errorf("whatsmeow.Next: %w", domain.ErrDisconnected)
    }
}

func (a *Adapter) Ack(id domain.EventID) error {
    return nil  // in-process adapter has no durable cursor; daemon (feature 004) wraps with a persistent cursor
}
```

## Lifecycle states (state machine)

```text
NotOpen
   │ Open(...)
   ▼
Opened, NotConnected ◀──── events.LoggedOut (sessionCleared)
   │
   │ Pair(ctx, "")  → QR via GetQRChannel
   │ Pair(ctx, phone)  → PairPhone(...)
   ▼
Paired, Connecting
   │ events.Connected
   ▼
Paired, Connected ──── events.Disconnected ────▶ Paired, Reconnecting
   │                                                      │
   │                                                      │ events.Connected
   │                                                      ▼
   │                                              Paired, Connected
   │
   │ Adapter.Close()
   ▼
Closed (terminal — every method returns ErrDisconnected)
```

## Forbidden patterns

| Pattern | Why forbidden |
|---|---|
| `import "go.mau.fi/whatsmeow"` from any non-`whatsmeow` adapter file | depguard `core-no-whatsmeow` rule |
| Returning `whatsmeow.SendResponse` from any port method | leaks library types into the core |
| Calling `client.SendMessage(ctx, ...)` with the caller's `ctx` instead of `a.clientCtx` | the aldinokemal mid-pair-cancel bug |
| `panic()` outside `recoverPanic` boundaries | `forbidigo` rule from `.golangci.yml` |
| `fmt.Print*` anywhere in the adapter | use `recordAudit` instead |
| Storing `context.Context` on a struct field except `clientCtx` (the documented exception) | Go stdlib code review comments |
| Adding a 9th port | requires the same procedure spec.md "Edge Cases" defines for adding the 8th |

## Test coverage

| Layer | Covered by | Count |
|---|---|---|
| JID translator | `translate_jid_test.go` | ~10 cases |
| Event translator | `translate_event_test.go` | ~15 cases (one per supported event type, plus error/unknown paths) |
| Send validation | `send_test.go` | ~6 cases (happy, ErrDisconnected, ErrEmptyBody, ErrMessageTooLarge, ErrInvalidJID, ctx cancelled) |
| EventStream | `stream_test.go` | ~6 cases (drain, ctx cancelled, clientCtx cancelled, full channel drop, parallel readers) |
| HistoryStore | `history_test.go` + `porttest/historystore.go` | HS1–HS6 |
| flock contention | `sqlitestore/store_test.go` + `sqlitehistory/store_test.go` | ~3 cases each |
| Full porttest contract suite | `adapter_integration_test.go` (//go:build integration) | all 8 ports × ~4 cases avg ≈ 30 cases |
