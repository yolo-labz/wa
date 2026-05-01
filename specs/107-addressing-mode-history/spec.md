# Feature 107 — addressing-mode preservation in history schema

**Branch**: `107-addressing-mode-history`
**Status**: implemented
**Source**: spec 105 / 106 follow-up. After 105 made LIDs first-class
addressable JIDs and 106 added the `IdentityResolver` port, the daemon
still **threw away** the alternate-namespace JID and the addressing
mode whatsmeow surfaces on every inbound `*events.Message`. This
feature persists both so callers (the AI agent surface) can render
either form without a runtime resolver round-trip.

## Problem

`whatsmeow/types.MessageInfo` carries:

- `AddressingMode` — `"pn"` or `"lid"`, indicating the namespace the
  sender was addressed by on the wire (per
  `whatsmeow/types/message.go:14-19`).
- `SenderAlt` — the alternate-namespace JID for `Sender` (per
  `whatsmeow/types/message.go:34`).

Pre-spec-107, `internal/adapters/secondary/whatsmeow/adapter.go`'s
`persistInboundMessage` extracted only `Chat`, `Sender`, `ID`,
`Timestamp`, `PushName`, `IsFromMe`, the body/media payload, and the
raw protobuf — silently dropping both addressing-mode fields. The
agent surface (`wa messages --json`) had no way to see "this LID
message also has a phone number `+5511...`" without an explicit
`contact.resolve` call per row.

## Decision

**Schema migration v4 → v5** adds two nullable columns on `messages`:

| column            | type | semantics                                                  |
| ----------------- | ---- | ---------------------------------------------------------- |
| `sender_alt_jid`  | TEXT | alternate-namespace JID; NULL when whatsmeow has no mapping |
| `addressing_mode` | TEXT | `"pn"` or `"lid"`; NULL on legacy and history-sync rows    |

Both are NULL-allowed because:

1. Legacy rows (pre-v5) do not have these values.
2. History-sync (`HistorySyncMsg`) carries no addressing mode.
3. Outbound rows persisted via `Send` have no inbound metadata.
4. Fresh-discovery contacts where whatsmeow has not yet mapped both
   namespaces leave the alt JID empty (this is the privacy property
   spec 106 FR-002 enshrines — callers MUST treat absence as
   "unknown" without erroring).

`InsertRaw` gains two trailing string parameters (`senderAltJID`,
`addressingMode`). Empty strings hit the database as NULL via an
`any` cast at the insertion site; `SELECT … COALESCE(col, '')`
ensures Scan never sees nil — callers always read empty strings for
"unknown".

`StoredMessage` gains two corresponding fields (`SenderAltJID`,
`AddressingMode`) that round-trip through `Insert` / `QueryHistory` /
`QueryMessages` / `QuerySearch` / `ExportChat`.

**Wire shape**: `wireMessage` (the JSON DTO for `messages`, `history`,
`search`, `export`) gains two `omitempty` fields:

```jsonc
{
  "messageId":      "MSG-LID-1",
  "chatJid":        "120363042199654321@g.us",
  "senderJid":      "66448177246461@lid",
  "timestamp":      1700000000,
  "body":           "hi",
  "isFromMe":       false,
  "pushName":       "Ricardo",
  "senderAltJid":   "5511999999999@s.whatsapp.net",  // new
  "addressingMode": "lid"                             // new
}
```

Both use `json:",omitempty"` so legacy rows produce wire envelopes
byte-identical to the pre-spec-107 shape — additive change, no schema
version bump on the `wa.messages/v1` NDJSON stream identifier.

## Alternatives rejected

Per Constitution rule 20.

### A. Bump NDJSON schema to `wa.messages/v2`

The two new fields could be guarded behind a version bump. **Rejected**
because the change is purely additive and `omitempty` keeps the
pre-spec wire shape byte-identical when both fields are empty. Bumping
the schema would force every consumer (CLI golden tests, the Claude
Code plugin's MCP shim, any future REST adapter) through a coordinated
upgrade for zero breaking change. Constitution rule 1 ("smallest change
that satisfies the request") applies.

### B. Resolve the alt JID lazily at query time via `IdentityResolver`

`messages` could omit the alt JID and the wire layer could call
`d.identity.ResolveLID` / `ResolvePN` on every row before serialising.
**Rejected** because:

1. WhatsApp's `MessageInfo` carries the per-row mapping authoritatively
   for the moment the message arrived; a later resolve may surface a
   *different* mapping if whatsmeow later learns of a re-pair, which
   would silently rewrite history. Persisting at insert time pins the
   mapping to the row, matching Constitution rule 24 (domain invariants
   encoded as data, not derived).
2. Lazy resolution adds an N×O(SQL-lookup) cost to every `wa messages
   --limit 200` call, which ships JSON over a unix socket and is in the
   Claude Code plugin's hot path.
3. The agent surface needs to render past messages with the addressing
   mode they originally had, not a current mapping — for audit trail
   coherence.

### C. Single `addressing_jids JSON` column

Both fields could collapse into a JSON blob. **Rejected** because:

1. SQLite JSON1 is available but `WHERE addressing_mode = 'lid'` is
   trivial today and would become `WHERE json_extract(addressing_jids,
   '$.mode') = 'lid'` — slower and unindexable.
2. The two fields are co-equal first-class metadata, not a varying
   subdocument. Two columns is the schema-honest representation.

## Functional requirements

- **FR-001** — Migration `v4→v5` adds `sender_alt_jid TEXT` and
  `addressing_mode TEXT` columns to `messages`, both NULL-allowed.
  `PRAGMA user_version` reads `5` after migration. Idempotent — a
  re-run against an already-migrated database is a no-op.
- **FR-002** — `InsertRaw` and `Insert` round-trip the two new fields
  through `QueryHistory` / `QueryMessages` / `QuerySearch` /
  `ExportChat`. Empty strings on input store NULL on disk; reads
  surface NULL as empty strings via `COALESCE`.
- **FR-003** — `persistInboundMessage` (in
  `internal/adapters/secondary/whatsmeow/adapter.go`) reads
  `wmEvt.Info.AddressingMode` (cast to string) and
  `wmEvt.Info.SenderAlt` (`.String()` if non-empty, else `""`),
  forwarding both to `InsertRaw`.
- **FR-004** — `persistOneMessage` (history-sync path) and the send
  path's outbound persistence pass empty strings — `HistorySyncMsg`
  carries no addressing mode, and outbound rows have no inbound
  metadata. The query layer COALESCEs to empty so callers see no
  difference between "nil column" and "empty input".
- **FR-005** — `wireMessage` JSON DTO gains
  `senderAltJid,omitempty` and `addressingMode,omitempty`. Pre-spec-107
  rows (NULL → empty → omitempty drop) produce wire envelopes
  byte-identical to the v1 shape.

## Tests

- `internal/adapters/secondary/sqlitehistory/migrate_v5_test.go`
  - `TestMigrateV5_AddsColumns` pins FR-001 against the full v2/v3/v4/v5
    chain.
  - `TestMigrateV5_Idempotent` pins re-run safety.
  - `TestStore_PersistsAndRetrievesAddressingMode` pins FR-002 round
    trip through `InsertRaw` + `QueryHistory` for both populated and
    empty rows.
- `internal/adapters/secondary/whatsmeow/persist_addressing_mode_test.go`
  - `TestPersistInbound_PropagatesAddressingMode` pins FR-003.
  - `TestPersistInbound_EmptyAltAndModeWhenAbsent` pins FR-003 fallback
    when whatsmeow has no mapping yet.
- `internal/adapters/secondary/sqlitehistory/migrate_v3_test.go` —
  existing chain test updated to assert `user_version = 5` (was 4).

## Out of scope

- **`AddressingMode` on outbound rows.** The send path persists
  outbound rows for history coherence; whatsmeow does not surface an
  outbound addressing mode (the daemon picked the recipient
  namespace itself). Skipping is correct.
- **Auto-`RecordMapping`.** When `persistInboundMessage` learns a
  fresh PN ↔ LID pair, it could call `IdentityResolver.RecordMapping`
  to seed the resolver cache. This is the spec 106 "out of scope"
  follow-up and lands in its own PR — wiring it requires plumbing the
  resolver into the adapter, which expands the blast radius of this
  change.
- **History-sync addressing mode.** WhatsApp's HistorySync protocol
  may eventually carry addressing-mode metadata (it does not today as
  of `whatsmeow@7514259`). The schema is ready to accept it the day
  the upstream payload exposes it.

## References

- `whatsmeow/types/message.go:14-19,33-34` — `AddressingMode` constants
  and `MessageSource.SenderAlt` field.
- `whatsmeow/types/message.go:96-114` — `MessageInfo` struct (embeds
  `MessageSource`).
- Spec 105 — domain JID parser accepts LID.
- Spec 106 — `IdentityResolver` port + `contact.resolve` JSON-RPC.
