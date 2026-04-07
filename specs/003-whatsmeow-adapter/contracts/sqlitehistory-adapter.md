# Contract: `sqlitehistory` adapter

**Applies to**: `internal/adapters/secondary/sqlitehistory/*.go` and `messages.db`
**Enforced by**: spec FR-005, FR-006, FR-007, FR-019, FR-020; the contract test suite at `internal/app/porttest/historystore.go`.

This file is the **schema, persistence, FTS5, and concurrency contract** for the message-history adapter. It complements `historystore.md` (the port behavioural contract) and `whatsmeow-adapter.md` (the upstream-facing translation contract).

## Universal rules

1. **Owns its own database file.** `$XDG_DATA_HOME/wa/messages.db`. NOT shared with whatsmeow's `session.db` (Cockburn principle: each adapter owns its persistence).
2. **CGO-free.** `modernc.org/sqlite` is the only SQLite driver. FTS5 is enabled via the `sqlite_fts5` build tag (research §D2).
3. **Single instance via `lockedfile`.** Acquired in `Open`, released in `Close`. The lock primitive is `github.com/rogpeppe/go-internal/lockedfile.Edit` (the same code the Go toolchain uses for module-cache locking — see research §D6), NOT raw `syscall.Flock`. A second `Open` against the same path returns a typed "history locked" error.
4. **No domain logic.** This package translates `domain.Message` → SQL row and back. Validation lives in `internal/domain` (feature 002).
5. **Schema is embedded.** `//go:embed schema.sql` per research §D5.

## Public surface

```go
package sqlitehistory

import (
    "context"
    "database/sql"

    "github.com/yolo-labz/wa/internal/domain"
)

type Store struct { /* unexported fields */ }

func Open(ctx context.Context, path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
func (s *Store) Insert(ctx context.Context, msgs []domain.Message) error
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.SearchHit, error)
```

`Insert` is package-private contract (called only by the whatsmeow adapter when delivering history sync results). `LoadMore` satisfies the `app.HistoryStore` port. `Search` is the FTS5 query path used by the future `wa search` CLI in feature 005 (returns `[]domain.SearchHit` which is a typed wrapper around `domain.Message` with a `Score float32` field — added to feature 002's domain in this commit, ONE additional sentinel).

Wait — that adds a third domain modification. To stay strictly within the 2-modification budget from the spec, **`Search` returns `[]domain.Message`** at the cost of losing the FTS5 score. Score is reintroduced in feature 005 if needed. The schema still computes it; it just isn't surfaced.

Updated public surface:

```go
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.Message, error)
```

## `Open` sequence

1. `os.MkdirAll(filepath.Dir(path), 0o700)`
2. Create the lock file `path + ".lock"` if it does not exist (`os.OpenFile` with `0o600`)
3. Acquire `lockedfile.Edit(lockPath)` — non-blocking; return wrapped "history locked" error on failure (research §D6 — uses `rogpeppe/go-internal/lockedfile`, the same primitive the Go toolchain uses)
4. `sql.Open("sqlite", path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")`
5. `os.Chmod(path, 0o600)`
6. Run `schemaSQL` (embedded) inside a transaction — idempotent via `CREATE ... IF NOT EXISTS`
7. Verify FTS5 is available: `SELECT fts5(?)` against `messages_fts` (synthetic check); error if missing
8. Return `*Store`

## SQL schema (`schema.sql`)

(See `data-model.md` §"SQL schema" for the full text. The schema is the canonical source; this contract does not duplicate it.)

**Invariants enforced by the schema**:

- `(chat_jid, message_id)` UNIQUE → idempotent re-inserts on history sync re-delivery
- `idx_messages_chat_ts` → O(log n) range queries on `(chat_jid, ts)` for `LoadMore`
- FTS5 triggers on `INSERT`/`UPDATE`/`DELETE` keep the virtual table in sync
- `tokenize='unicode61 remove_diacritics 2'` → accent-insensitive search (Brazilian Portuguese requirement)

## `LoadMore` SQL

```sql
SELECT message_id, chat_jid, sender_jid, ts, body, raw_proto
FROM messages
WHERE chat_jid = ?
  AND (? = '' OR ts < (SELECT ts FROM messages WHERE chat_jid = ? AND message_id = ?))
ORDER BY ts DESC
LIMIT ?
```

The empty-string sentinel for `before` means "start from newest". The subquery resolves the `before` cursor's timestamp, then the outer query takes everything strictly older. Subquery is O(log n) via the unique index.

## `Insert` SQL

```sql
INSERT INTO messages (chat_jid, sender_jid, message_id, ts, body, raw_proto)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, message_id) DO NOTHING
```

`ON CONFLICT DO NOTHING` is the idempotency clause — re-delivering the same message from a history sync notification is a no-op. The trigger fires on `INSERT` only when the row is actually inserted, so FTS5 stays consistent with the canonical table.

`Insert` is called with a slice; the implementation runs the inserts inside a single transaction for throughput. A 1000-message batch should complete in <100ms on commodity hardware.

## `Search` SQL

```sql
SELECT m.message_id, m.chat_jid, m.sender_jid, m.ts, m.body, m.raw_proto
FROM messages_fts
JOIN messages m ON m.rowid = messages_fts.rowid
WHERE messages_fts MATCH ?
ORDER BY rank
LIMIT ?
```

`MATCH` uses FTS5's tokenizer; the caller passes a free-text query. `ORDER BY rank` returns the FTS5-computed BM25 score order without exposing the score in the return type.

## Concurrency contract

| Operation | Concurrency | Mechanism |
|---|---|---|
| `LoadMore` from N goroutines | Safe; parallel | `database/sql` connection pool + `READ UNCOMMITTED` semantics suffice for read-only queries |
| `Insert` from one goroutine while `LoadMore` runs | Safe; reads see committed state | WAL mode |
| `Insert` from N goroutines | Safe but serialised at the SQLite level | WAL mode permits concurrent readers + one writer; multiple writers serialise via SQLite's per-DB write lock |
| `Open` twice in the same process | Second call fails at `flock` | `LOCK_EX | LOCK_NB` |
| `Close` while `LoadMore` is in flight | Caller's responsibility to drain first | `Close` returns an error if connections are still in use |

## Error categories

| Error | When |
|---|---|
| `wrapped("history locked: %s", path)` | `Open` if another process holds the flock |
| `wrapped(domain.ErrInvalidJID, ...)` | `LoadMore` / `Insert` on zero JID |
| `wrapped("invalid limit: %d")` | `LoadMore` with `limit <= 0` |
| `context.Canceled` / `context.DeadlineExceeded` | Caller cancellation |
| `wrapped("sqlite: %v", err)` | Any underlying SQL failure |
| `wrapped("fts5 not available")` | `Open` if FTS5 is somehow disabled in the build |

Never `domain.ErrDisconnected` — that sentinel is reserved for `MessageSender.Send`.

## File permissions

| Path | Mode | Set by |
|---|---|---|
| `$XDG_DATA_HOME/wa/` | `0700` | `Open` (`os.MkdirAll`) |
| `$XDG_DATA_HOME/wa/messages.db` | `0600` | `Open` (`os.Chmod` after open) |
| `$XDG_DATA_HOME/wa/messages.db.lock` | `0600` | `Open` (created with `os.OpenFile(..., 0o600)`) |
| `$XDG_DATA_HOME/wa/messages.db-shm` (WAL shared memory) | `0600` (inherited) | SQLite |
| `$XDG_DATA_HOME/wa/messages.db-wal` (WAL log) | `0600` (inherited) | SQLite |

## Test coverage

| Layer | File | Count |
|---|---|---|
| Schema bootstrap idempotency | `store_test.go` | 2 (first Open creates; second Open is no-op) |
| `LoadMore` happy path | `store_test.go` | ~5 (empty store, single chat, multiple chats, before cursor, limit clamp) |
| `Insert` idempotency | `store_test.go` | 2 (re-insert is no-op; FTS5 stays consistent) |
| `Search` FTS5 | `store_test.go` | ~6 (literal match, prefix match, multi-word, accent insensitivity, no match, limit) |
| `flock` contention | `flock_test.go` | 3 (single open OK, second open fails, after Close second Open OK) |
| Concurrency | `store_test.go` | 2 (parallel LoadMore + Insert under `-race`) |
| Full porttest contract | via the whatsmeow adapter's integration test | HS1–HS6 (HS3 only — in-memory branch) |

## Forbidden patterns

| Pattern | Why |
|---|---|
| `import "go.mau.fi/whatsmeow"` | depguard rule scoping; this adapter is whatsmeow-agnostic |
| Storing `whatsmeow.MessageInfo` directly in the table | wrong layer; we store the bytes via `raw_proto` blob |
| Using `mattn/go-sqlite3` (CGO-required driver) | Constitution Principle IV |
| Disabling WAL or `synchronous=NORMAL` for "performance" | data integrity under crash > write throughput |
| Embedding the schema as a `const string` instead of `//go:embed` | research §D5 forbids it |
| Adding new SQL tables without bumping the schema embed file | future migrations need a versioned `schema_v2.sql` |
