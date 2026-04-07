# Data Model: whatsmeow Secondary Adapter

**Branch**: `003-whatsmeow-adapter` · **Plan**: [`plan.md`](./plan.md) · **Research**: [`research.md`](./research.md)

This is the **Go-level** data model for feature 003. It documents (a) the **two minimal modifications** to feature 002's locked artefacts (`internal/domain/errors.go` and `internal/app/ports.go`), (b) the new `sqlitehistory` adapter struct and SQL schema, and (c) the whatsmeow Adapter struct layout. Every type is pure-Go where possible; whatsmeow types appear ONLY inside `internal/adapters/secondary/whatsmeow/`.

## Modification 1 — `internal/domain/errors.go` (one new line)

```go
// internal/domain/errors.go
//
// (existing six sentinels from feature 002 unchanged)

// ErrDisconnected is returned by MessageSender.Send when the underlying
// adapter is in a disconnected state. The caller decides whether to retry,
// queue, or surface the failure; the adapter never queues silently.
//
// Added by feature 003 (whatsmeow secondary adapter) to support FR-018.
var ErrDisconnected = errors.New("domain: adapter disconnected")
```

**Test row**: one new row in the existing `errors_test.go` table test asserting `errors.Is(fmt.Errorf("send: %w", ErrDisconnected), ErrDisconnected)` returns true.

**This is the ONLY modification to `internal/domain/`** in this feature. SC-002 in `spec.md` was softened to permit it explicitly.

## Modification 2 — `internal/app/ports.go` (one new interface)

```go
// internal/app/ports.go
//
// (existing seven port interfaces from feature 002 unchanged: MessageSender,
// EventStream, ContactDirectory, GroupManager, SessionStore, Allowlist, AuditLog)

// HistoryStore is the secondary port for historical message lookup. It is the
// 8th port, added by feature 003 per CLAUDE.md §"Reliability principles" rule
// 20 (Cockburn: ports as intent of conversation, no fixed count).
//
// Implementations MUST:
//   - Return at most `limit` messages older than `before` in the given chat,
//     ordered by timestamp descending.
//   - First read from local persistence (e.g. messages.db); if fewer than
//     `limit` messages are available locally and the underlying transport
//     supports on-demand backfill, fetch the remainder.
//   - Return an empty slice and nil error when no more messages exist (NOT
//     a typed error — empty is the success case for "you have everything").
//   - Honour ctx cancellation; long-running on-demand fetches MUST be
//     cancellable.
//   - Be safe for concurrent reads from multiple goroutines.
//
// Behavioural contract: see specs/003-whatsmeow-adapter/contracts/historystore.md
type HistoryStore interface {
    LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
}
```

**Contract test**: `internal/app/porttest/historystore.go` extends the existing suite with HS1–HS6 cases (defined in `contracts/historystore.md`).

**This is the ONLY modification to `internal/app/`** in this feature. The new `porttest/historystore.go` file is additive; it does not modify any existing porttest file.

## New entity 1 — `sqlitehistory.Store`

```go
// internal/adapters/secondary/sqlitehistory/store.go
package sqlitehistory

import (
    "context"
    "database/sql"
    _ "modernc.org/sqlite"

    "github.com/yolo-labz/wa/internal/domain"
)

type Store struct {
    db       *sql.DB
    path     string
    lockFile *os.File // held by flock for the lifetime of Store
}

func Open(ctx context.Context, path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
func (s *Store) Insert(ctx context.Context, msgs []domain.Message) error  // package-private contract; the whatsmeow adapter calls this on history sync delivery
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.Message, error)  // FTS5 query — used by future feature 005's `wa search`
```

`Open` performs three operations atomically: (a) `mkdir -p ~/.local/share/wa` with `0700`, (b) `flock(LOCK_EX|LOCK_NB)` on `path`, (c) `sql.Open` and run `schemaSQL`. Failure at any step returns a typed error and unwinds.

`Close` releases the file lock and closes the SQL handle. Callers MUST `defer Close()`.

## SQL schema — `sqlitehistory/schema.sql`

```sql
-- Embedded via go:embed; executed once on first Open() (idempotent).

CREATE TABLE IF NOT EXISTS messages (
    rowid       INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_jid    TEXT NOT NULL,
    sender_jid  TEXT NOT NULL,
    message_id  TEXT NOT NULL,
    ts          INTEGER NOT NULL,           -- unix nanoseconds
    body        TEXT NOT NULL DEFAULT '',
    raw_proto   BLOB,                       -- whatsmeow's raw waE2E.Message protobuf, gzipped
    UNIQUE (chat_jid, message_id)
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages (chat_jid, ts DESC);

-- FTS5 virtual table over the body column. Content lives in messages.body;
-- the FTS5 table only stores tokens + rowid pointers (no duplication).
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    body,
    content='messages',
    content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);

-- Sync triggers: keep messages_fts in lockstep with messages.
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES (new.rowid, new.body);
END;
```

**Schema invariants**:

- `(chat_jid, message_id)` is unique → idempotent inserts on history sync re-delivery
- `ts` is unix nanoseconds (matches `domain.Event.Timestamp().UnixNano()`)
- `raw_proto` is gzipped to halve disk usage; uncompressed via `gzip.NewReader` on read. **Rationale (per research §D13)**: round-trip preservation enables future re-translation when the event translator gains new fields (e.g. `IsEdited`, `QuotedMessageID`) without re-fetching from WhatsApp. **Cost**: ~2× per row for short messages, ~1.1× for long ones (gzip handles repetitive protobuf framing well); ~50µs decode latency per row, paid only on schema migration since `LoadMore` and `Search` read `body` directly.
- `messages_fts` uses `tokenize='unicode61 remove_diacritics 2'` so accented characters match unaccented queries (Brazilian Portuguese requirement — the user's primary language). The `2` argument is the diacritic-removal level (0=none, 1=unicode-codepoint-only, 2=full Unicode normalisation including combining marks).

## New entity 2 — `whatsmeow.Adapter` struct

```go
// internal/adapters/secondary/whatsmeow/adapter.go
package whatsmeow

import (
    "context"
    "sync"

    waClient "go.mau.fi/whatsmeow"
    "go.mau.fi/whatsmeow/store/sqlstore"
    "go.mau.fi/whatsmeow/types/events"
    "go.mau.fi/whatsmeow/proto/waHistorySync"

    "github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
    "github.com/yolo-labz/wa/internal/adapters/secondary/sqlitestore"
    "github.com/yolo-labz/wa/internal/domain"
)

// whatsmeowClient is the package-private interface the Adapter consumes.
// Production constructs from *waClient.Client; tests use a hand-rolled fake.
// Per research §D4.
type whatsmeowClient interface {
    Connect() error
    Disconnect()
    IsConnected() bool
    IsLoggedIn() bool
    Logout(ctx context.Context) error
    SendMessage(ctx context.Context, to waTypes.JID, msg *waE2E.Message, ...) (whatsmeow.SendResponse, error)
    GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
    PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType whatsmeow.PairClientType, clientDisplayName string) (string, error)
    BuildHistorySyncRequest(lastKnownMessageInfo *waTypes.MessageInfo, count int) *waE2E.Message
    DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, untrustedSource bool) (*waHistorySync.HistorySync, error)
    AddEventHandler(handler whatsmeow.EventHandler) uint32
    Store() *store.Device  // for the JID + DeviceProps access
}

// Adapter is the secondary adapter satisfying all eight port interfaces from
// internal/app/ports.go. Constructed via Open() in the daemon's composition
// root (feature 004).
type Adapter struct {
    client      whatsmeowClient
    store       *sqlstore.Container       // whatsmeow's session ratchet store, owned by sqlitestore package
    history     *sqlitehistory.Store      // OUR message history store
    allowlist   *domain.Allowlist         // shared across the daemon (feature 004 wires it)
    auditBuf    *auditRingBuffer          // in-memory ring buffer for v0; feature 004 swaps for slogaudit
    eventCh     chan domain.Event         // bounded buffer (capacity 256, defended by mautrix RECENT-burst observation in Clarifications session 2026-04-07 round 2) for EventStream.Next
    eventSeq    atomic.Uint64             // monotonic source for domain.EventID
    historyReqs sync.Map                  // map[string]chan *waHistorySync.HistorySync — request ID → response (research §D1; xsync.MapOf is a typed alternative if perf becomes a concern, see research §D1 alternatives)
    clientCtx   context.Context           // daemon-scoped, cancelled only at Close (FR-012)
    clientCancel context.CancelFunc
    closed      atomic.Bool
}

func Open(parentCtx context.Context, sessionStorePath, historyStorePath string, allowlist *domain.Allowlist) (*Adapter, error)
func (a *Adapter) Close() error
func (a *Adapter) Pair(ctx context.Context, phone string) error  // QR by default; phone code if non-empty
```

**Construction order in `Open`**:

1. Open `sqlitestore.Container` (whatsmeow ratchets) at `sessionStorePath` with `flock`
2. Open `sqlitehistory.Store` at `historyStorePath` with `flock` (separate file lock)
3. Mutate `device.DeviceProps.HistorySyncConfig` to `{Days: 7, Size: 20MB, Quota: 100MB}` per FR-019
4. Construct `*waClient.Client` with the 12 production flags from FR-009
5. Set `client.ManualHistorySyncDownload = true`
6. Register event handler via `client.AddEventHandlerWithSuccessStatus(a.handleWAEvent)`
7. Allocate `eventCh` with cap 256 (per Clarifications session 2026-04-07 round 2), `historyReqs` empty `sync.Map`, `auditBuf` with cap 1000
8. Create `clientCtx` from `context.Background()` (NOT parentCtx — see FR-012)
9. Return `*Adapter`

**`Close` order**:

1. Cancel `clientCtx` (signals event handler to drain)
2. `client.Disconnect()` (whatsmeow shuts down the websocket)
3. Drain `eventCh` (consumers must have stopped calling `Next` by now)
4. `history.Close()` (releases history.db flock)
5. `store.Close()` (releases session.db flock)
6. Set `closed = true` so further calls return `domain.ErrDisconnected`

## Lifecycle states

```text
NotOpen
   │ Open(ctx, sessionPath, historyPath, allowlist)
   ▼
Opened, NotConnected
   │ Pair(ctx, "")  → QR flow
   │ Pair(ctx, "+5511...")  → phone-code flow
   ▼
Paired, Connected ──── events.LoggedOut ────▶ Opened, NotConnected (re-pair required)
   │
   │ Adapter.Close()
   ▼
Closed (terminal — all calls return ErrDisconnected)
```

## slog → waLog bridge (`log.go`)

```go
// internal/adapters/secondary/whatsmeow/log.go
package whatsmeow

import (
    "log/slog"
    "go.mau.fi/whatsmeow/util/log"
)

// slogWALog adapts a *slog.Logger into whatsmeow's waLog.Logger interface.
// Per research §D10. There is exactly ONE such bridge in the project; do not
// reinvent it per file.
type slogWALog struct {
    log *slog.Logger
}

func newSlogWALog(l *slog.Logger) waLog.Logger { return &slogWALog{log: l} }

func (s *slogWALog) Debugf(msg string, args ...any) { s.log.Debug(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Infof(msg string, args ...any)  { s.log.Info(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Warnf(msg string, args ...any)  { s.log.Warn(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Errorf(msg string, args ...any) { s.log.Error(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Sub(module string) waLog.Logger {
    return &slogWALog{log: s.log.With("module", module)}
}
```

The bridge is constructed once in `Open()` from the `*slog.Logger` the daemon (feature 004) passes via `Open` arguments. No goroutine, no allocation per call beyond `fmt.Sprintf`.

## Audit ring buffer

```go
// internal/adapters/secondary/whatsmeow/audit.go
package whatsmeow

type auditRingBuffer struct {
    mu     sync.Mutex
    buf    []domain.AuditEvent
    head   int
    cap    int
}

func newAuditRing(cap int) *auditRingBuffer
func (r *auditRingBuffer) Record(ctx context.Context, e domain.AuditEvent) error  // satisfies AuditLog port
func (r *auditRingBuffer) Snapshot() []domain.AuditEvent  // for tests
```

A 1000-entry circular buffer. Wraps around silently — old entries are overwritten, never dropped to disk. Feature 004 replaces this with `internal/adapters/secondary/slogaudit/` which writes to `$XDG_STATE_HOME/wa/audit.log` per CLAUDE.md §"Safety".

## Invariants and where they live

| Invariant | Enforced by | File |
|---|---|---|
| `whatsmeow/types.JID` does not escape the adapter package | `core-no-whatsmeow` depguard rule + manual review | `.golangci.yml` + `translate_jid.go` |
| `clientCtx` is daemon-scoped, never request-scoped | constructor in `Open` | `adapter.go` |
| `messages.db` is FTS5-enabled | build tag `sqlite_fts5` + schema | `sqlitehistory/schema.sql` |
| Two flocks (session + messages) acquired in order | `Open` constructor | `adapter.go` |
| File permissions `0600` on both DBs, `0700` on parent dir | `os.Chmod` after `mkdir` | `sqlitestore/store.go` + `sqlitehistory/store.go` |
| No goroutines outside the event handler + history processor | code review + the `noctx` lint check | adapter package |
| `events.LoggedOut` clears the session and emits `PairFailure` | `handleWAEvent` switch | `translate_event.go` |
| `Send` returns `ErrDisconnected` when `closed=true` or `!IsConnected()` | `send.go` | `send.go` |

## Per-file LOC budget (CHK047)

| Path | Budget | Rationale |
|---|---|---|
| `internal/adapters/secondary/whatsmeow/adapter.go` | ~250 LoC | Construction, Close, accessor methods, struct definition |
| `internal/adapters/secondary/whatsmeow/pair.go` | ~150 LoC | QR + phone code flows, 3-min detached context |
| `internal/adapters/secondary/whatsmeow/send.go` | ~80 LoC | MessageSender impl + ErrDisconnected handling |
| `internal/adapters/secondary/whatsmeow/stream.go` | ~60 LoC | EventStream impl over bounded channel |
| `internal/adapters/secondary/whatsmeow/translate_jid.go` | ~80 LoC | toDomain + toWhatsmeow + helpers + panic-on-zero |
| `internal/adapters/secondary/whatsmeow/translate_event.go` | ~250 LoC | Switch table for 8 event types + per-variant translators |
| `internal/adapters/secondary/whatsmeow/directory.go` | ~80 LoC | ContactDirectory impl |
| `internal/adapters/secondary/whatsmeow/groups.go` | ~80 LoC | GroupManager impl |
| `internal/adapters/secondary/whatsmeow/session.go` | ~50 LoC | SessionStore impl (delegates to sqlitestore) |
| `internal/adapters/secondary/whatsmeow/allowlist.go` | ~30 LoC | Wraps *domain.Allowlist |
| `internal/adapters/secondary/whatsmeow/audit.go` | ~100 LoC | In-memory ring buffer |
| `internal/adapters/secondary/whatsmeow/history.go` | ~250 LoC | HistoryStore impl + on-demand BuildHistorySyncRequest plumbing + sync.Map management |
| `internal/adapters/secondary/whatsmeow/log.go` | ~50 LoC | slogWALog bridge type per D10 |
| `internal/adapters/secondary/whatsmeow/flags.go` | ~60 LoC | The 12 production whatsmeow client flag constants |
| `internal/adapters/secondary/sqlitestore/store.go` | ~150 LoC | *sqlstore.Container wrapper + lockedfile lock |
| `internal/adapters/secondary/sqlitehistory/store.go` | ~250 LoC | Open, Close, LoadMore, Insert, Search + lockedfile lock |
| `internal/adapters/secondary/sqlitehistory/schema.sql` | ~40 lines | Embedded SQL (not LoC but counted) |
| `internal/adapters/secondary/sqlitehistory/schema_embed.go` | ~10 LoC | //go:embed declaration |
| `internal/app/porttest/historystore.go` | ~150 LoC | HS1–HS6 contract test cases |
| **Adapter source total (sum of rows above)** | **~2070 LoC** | whatsmeow ~1550 + sqlitestore ~150 + sqlitehistory ~250 + porttest 150 + log/flags/audit/etc ~100. Within the 2200 SC-008 budget; ~130 LoC headroom (F6 fix from `/speckit:analyze`: previously asserted "~2120" without summing the rows; now reconciled by summation) |
| **Test files (excluded from SC-008 per the spec)** | ~1000 LoC | Unit tests for translators, send, stream, history, flock contention; the new `reconnect_bench_test.go` from T056 (F5 fix) is included |

## What this data model is NOT

- It is NOT a wire format. JSON-RPC DTOs for the daemon's socket layer live in feature 004.
- It is NOT a database migration framework. Schema upgrades land in `schema_v2.sql` in a future feature with a small migration runner.
- It is NOT exhaustive of every whatsmeow event type. Status, calls, business catalog, newsletter — all deferred. They get added incrementally.
- It does NOT define new ports beyond `HistoryStore`. Adding a 9th port requires the same procedure documented in feature 002 spec.md "Edge Cases".
