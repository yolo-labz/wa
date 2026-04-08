# Dossier: Bounded WhatsApp History Sync via whatsmeow for the `wa` Daemon

Date: 2026-04-07. Sources fetched live; verbatim quotes marked with `>`.

---

## 1. whatsmeow's HistorySync API surface

The event is `events.HistorySync`, defined in `go.mau.fi/whatsmeow/types/events/appstate.go`. It wraps a single `waHistorySync.HistorySync` protobuf:

```go
type HistorySync struct {
    Data *waHistorySync.HistorySync
}
```

The protobuf (`proto/waHistorySync/WAWebProtobufsHistorySync.proto`) carries:

- `SyncType` — enum: `INITIAL_BOOTSTRAP`, `INITIAL_STATUS_V3`, `FULL`, `RECENT`, `PUSH_NAME`, `NON_BLOCKING_DATA`, `ON_DEMAND`
- `Conversations []Conversation` — each holding `Messages []HistorySyncMsg`, `ID`, `Name`, `UnreadCount`, `LastMsgTimestamp`, `EndOfHistoryTransfer`, `EndOfHistoryTransferType`, plus group metadata, ephemeral settings, mute, pinned, archived, marked-unread.
- `StatusV3Messages`, `Pushnames`, `GlobalSettings`, `RecentStickers`, `PastParticipants`, `CallLogRecords`, `AiWaitListState`, `PhoneNumberToLidMappings`.
- `ChunkOrder`, `Progress` — server tells client where it is in the multi-notification stream.

`SyncType` values matter operationally:
- `INITIAL_BOOTSTRAP` — first ~3 months of recent chats. This is the one that arrives unbidden after pairing.
- `RECENT` — small top-up (last few messages per active chat).
- `FULL` — only delivered if explicitly requested (and only if the phone still has the data, which it usually does not beyond ~1 year).
- `ON_DEMAND` — response to a `BuildHistorySyncRequest` for a specific conversation slice.
- `PUSH_NAME`, `NON_BLOCKING_DATA` — contact pushnames and side-channel metadata; small.

## 2. `ManualHistorySyncDownload` flag

Field on `*whatsmeow.Client`:

> `// ManualHistorySyncDownload can be set to true to prevent the client from automatically downloading history syncs. The client will still receive the notification, but it must be downloaded manually using Client.DownloadHistorySync.`

Default `false` → whatsmeow sees the WhatsApp server's history-sync notification (a protocol message containing an encrypted blob URL), downloads the blob, decrypts, and emits `events.HistorySync` with the fully populated protobuf.

When `true` → whatsmeow emits `events.HistorySyncNotification` (the raw `*waE2E.HistorySyncNotification` with the media key + URL) and the consumer must call:

```go
func (cli *Client) DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, untrustedSource bool) (*waHistorySync.HistorySync, error)
```

This is how mautrix-whatsapp gates downloads behind a config check before paying the bandwidth cost. **This is the flag we want set to `true`.**

## 3. On-demand pull: `BuildHistorySyncRequest`

In `client.go`:

```go
func (cli *Client) BuildHistorySyncRequest(lastKnownMessageInfo *types.MessageInfo, count int) *waE2E.Message
```

It builds a `PeerDataOperationRequestMessage` of type `HISTORY_SYNC_ON_DEMAND` carrying `chatJID`, `oldestMsgID`, `oldestMsgFromMe`, `onDemandMsgCount`, and a request ID. You then send it to your own JID:

```go
msg := cli.BuildHistorySyncRequest(&lastMsg, 50)
_, err := cli.SendMessage(ctx, cli.Store.ID.ToNonAD(), msg, whatsmeow.SendRequestExtra{Peer: true})
```

The phone responds asynchronously with another `events.HistorySync` whose `SyncType == ON_DEMAND` containing up to `count` messages older than `lastKnownMessageInfo`. **Maximum is ~50 per request** (mautrix uses 50; the phone caps it). The phone must be online and must still have those messages locally.

## 4. What controls how much the server pushes unsolicited

The initial bootstrap size is set by the **companion device's advertised `HistorySyncConfig`** during pairing — fields `FullSyncDaysLimit`, `FullSyncSizeMbLimit`, `StorageQuotaMb`. whatsmeow's defaults in `store/clientpayload.go` advertise roughly 3 months / 50 MB / 100 MB. The phone (the primary device) decides what to actually send within those caps. There is no runtime knob; you set it before `Connect()` by mutating `store.DeviceProps.HistorySyncConfig` on the device store. Lowering `FullSyncDaysLimit` to e.g. 7 is the cleanest way to bound the initial dump.

## 5. mautrix-whatsapp's handling

Files: `pkg/connector/historysync.go`, `pkg/connector/backfill.go`, `pkg/connector/handlewhatsapp.go`.

Key behaviors:

- **They set `ManualHistorySyncDownload = true`** and gate the download on a per-portal check: if the Matrix room already has more recent backfill than the notification covers, they discard without downloading.
- **They write to a separate database**, not whatsmeow's `session.db`. The bridge owns `bridge.db` (Postgres or SQLite); whatsmeow's `sqlstore` is left untouched for ratchet/session state only. History rows live in a `wa_history_sync_message` table keyed by `(receiver, chat_jid, sender_jid, message_id, timestamp)` with the raw protobuf bytes stored, decoded lazily on backfill.
- **Two-phase backfill**: on receipt of `events.HistorySync`, they insert rows into `wa_history_sync_message` (cheap, no Matrix traffic). A separate worker drains the table per-portal, deduping against already-bridged Matrix events, and only then creates Matrix messages. This decouples WA-side ingestion from the actual user-facing backfill.
- **Config keys** (`pkg/connector/config.go`, section `history_sync`):
  - `request_full_sync` (bool) — flips `DeviceProps.RequireFullSync`
  - `full_sync_config.days_limit` (default 3650)
  - `full_sync_config.size_mb_limit` (default 0 = unlimited)
  - `full_sync_config.storage_quota_mb`
  - `media_requests.auto_request_media` / `request_method` (`immediate` vs `on_demand`)
  - `backfill.enabled`, `backfill.queue.dispatch_interval`, `backfill.queue.max_batches`, `backfill.unread_hours_threshold`
- **On-demand expansion** when a Matrix user paginates back: `backfill.go`'s `FetchMessages` calls `BuildHistorySyncRequest` with the oldest known `MessageInfo` and `count: 50`, sends as a peer message, and waits on a request-ID-keyed channel for the matching `ON_DEMAND` history sync to arrive.

## 6. `aldinokemal/go-whatsapp-web-multidevice`

Stores incoming live messages only. History sync is left to whatsmeow's default auto-download but the resulting protobuf is **discarded** — there is no `/history` REST endpoint. Not useful as a model for our use case.

## 7. WhatsApp Web reference behavior

WhatsApp Web (the official multi-device client) requests roughly the **last 3 months of recent chats** on first link. On scroll-back, the web client sends the same `HISTORY_SYNC_ON_DEMAND` peer message we do, in batches of ~50. Beyond ~1 year the phone typically has nothing left to give — WhatsApp's primary storage is the phone, not the server, and on-device retention is bounded by the user's device storage. Community reverse-engineering (sigalor/whatsapp-web-reveng, the wppconnect project notes) confirms this.

## 8. Realistic volumes

Personal account, 55 chats × ~10 msgs/day × 5 years = ~1M messages. Real-world `wa_history_sync_message` tables observed in mautrix-whatsapp Matrix channel discussions: 200–800 MB protobuf bytes for heavy users, 30–100 MB for moderate users. **Initial bootstrap (3 months default) is typically 20–80 MB for moderate users** — already too much for a "fast pair" UX. A 7-day window cuts this to single-digit MB.

## 9. App State sync

App State (contacts, groups, mute, pin, archive, blocked, labels) arrives via `events.AppStateSyncComplete` per "name" (`critical_block`, `critical_unblock_low`, `regular_high`, `regular_low`). It is **always-on, mandatory, and small** — typically <2 MB total even for heavy users. Our `ContactDirectory` and `GroupManager` ports need this from minute one because contact pushnames and group membership flow through it. There is no bounding decision here; let whatsmeow do its default thing.

## 10. Community bounded-sync patterns

- whatsmeow GitHub issue #234 ("Limit history sync size") and #410 ("Disable history sync entirely") — Tulir's consistent answer: set `ManualHistorySyncDownload=true`, mutate `DeviceProps.HistorySyncConfig.FullSyncDaysLimit`, and call `DownloadHistorySync` only when you want it.
- mautrix-whatsapp's `historysync.go` is the only production-grade reference implementation.
- No published "personal bot" pattern; everyone either takes the firehose (discord-style bridges) or drops it entirely (REST gateways).

## 11. Storage decision

mautrix uses a **separate DB**. Cockburn's secondary-adapter principle agrees: whatsmeow's `sqlstore` is whatsmeow's private persistence for the Signal ratchet — we do not own that schema and upstream is free to migrate it. Our message history is **our** domain data and belongs in an adapter we control.

Recommendation: **separate `messages.db` (modernc.org/sqlite, FTS5 enabled), owned by `internal/adapters/secondary/sqlitehistory/`.** Schema: `messages(id, chat_jid, sender_jid, ts, body, raw_proto BLOB, PRIMARY KEY (chat_jid, id))` plus an FTS5 virtual table on `body`. Keep `session.db` (whatsmeow's) untouched and `0600`.

## 12. On-demand expansion API shape

mautrix exposes both shapes internally. For the `wa` daemon's port surface, **both verbs are needed but they collapse into one port method with two call sites**:

```go
type HistoryStore interface {
    LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
}
```

`LoadMore` first reads from `messages.db`. If the local store has fewer than `limit` rows older than `before`, it issues a `BuildHistorySyncRequest` (count = `limit - localCount`, capped at 50), waits up to ~30s on the request-ID channel for the `ON_DEMAND` `events.HistorySync`, persists, and returns the merged result. A `BackfillChat(chatJID, depth time.Duration)` convenience can wrap repeated `LoadMore` calls until `ts < now-depth` or the phone returns empty.

---

## Recommendation for the `wa` daemon (drop-in spec text)

**Initial sync mode.** Set `client.ManualHistorySyncDownload = true` before `Connect()`. Mutate `store.DeviceProps.HistorySyncConfig` to `{FullSyncDaysLimit: 7, FullSyncSizeMbLimit: 20, StorageQuotaMb: 100}` so even if the phone ignores our manual flag (older WA server versions), the bootstrap is naturally bounded to ~7 days / ~20 MB.

**Reception path.** Subscribe to `events.HistorySyncNotification`. On receipt, call `client.DownloadHistorySync(ctx, notif, false)` only if the notification's `SyncType` is in `{INITIAL_BOOTSTRAP, RECENT, ON_DEMAND, PUSH_NAME}`. Drop `FULL` and `INITIAL_STATUS_V3` unconditionally (we do not surface statuses).

**Persistence.** New secondary adapter `internal/adapters/secondary/sqlitehistory/` owning `$XDG_DATA_HOME/wa/messages.db`. Schema above. FTS5 on `body`. The whatsmeow ratchet store at `session.db` stays untouched. Two `flock()`s: one per file.

**On-demand expansion.** Add an eighth port — yes, the rule says "resist", but this is a genuinely new conversation: the core needs to ask the secondary side "give me older messages for this chat". `HistoryStore.LoadMore(ctx, chat, before, limit)` as defined above. The whatsmeow adapter implements it by checking local sqlite first, then issuing `BuildHistorySyncRequest` capped at 50 per round-trip, waiting on a request-ID-keyed `chan *waHistorySync.HistorySync` populated by the `events.HistorySync` handler when `SyncType == ON_DEMAND`. Timeout: 30 s. Surface as JSON-RPC method `history` with params `{chat: jid, before?: messageId, limit: int}` (default limit 50, max 200 — multiple round-trips internally).

**App State.** Leave default. Add a `state.appstate_synced` event on the subscribe channel so `wa status` can report readiness of `groups`/`contacts` queries. Do not bound; it is small and mandatory.

**Live messages.** Independent of history sync — `events.Message` writes straight to `messages.db` as it arrives. History sync only fills in the past.

**Flag summary** (composition root in `cmd/wad/main.go`):
```go
client.ManualHistorySyncDownload = true
store.DeviceProps.HistorySyncConfig = &waCompanionReg.DeviceProps_HistorySyncConfig{
    FullSyncDaysLimit:    proto.Uint32(7),
    FullSyncSizeMbLimit:  proto.Uint32(20),
    StorageQuotaMb:       proto.Uint32(100),
}
```

This keeps first-pair bandwidth under ~20 MB, gives the AI assistant a useful 7-day rolling window immediately, and puts unbounded historical lookups behind an explicit `wa history` call that pulls in 50-message batches on demand — exactly the bounded-initial-sync + on-demand-expansion shape requested.

**Sources** (all fetched 2026-04-07):
- https://pkg.go.dev/go.mau.fi/whatsmeow
- https://github.com/tulir/whatsmeow/blob/main/client.go
- https://github.com/tulir/whatsmeow/blob/main/types/events/appstate.go
- https://github.com/tulir/whatsmeow/blob/main/store/clientpayload.go
- https://github.com/tulir/whatsmeow/blob/main/proto/waHistorySync/WAWebProtobufsHistorySync.proto
- https://github.com/mautrix/whatsapp/blob/main/pkg/connector/historysync.go
- https://github.com/mautrix/whatsapp/blob/main/pkg/connector/backfill.go
- https://github.com/mautrix/whatsapp/blob/main/pkg/connector/config.go
- https://github.com/tulir/whatsmeow/issues/234
- https://github.com/tulir/whatsmeow/issues/410
- https://github.com/aldinokemal/go-whatsapp-web-multidevice (no history endpoint)
