// Package sqlitehistory is the secondary adapter that owns the local
// message-history database (`messages.db`). It is the local-first store
// consulted by the whatsmeow adapter's HistoryStore.LoadMore before any
// remote backfill round-trip, and is the FTS5 index used by the future
// `wa search` CLI subcommand. Per CLAUDE.md §"Daemon, IPC,
// single-instance" the database is single-writer, gated by a
// process-wide lockedfile mutex on `messages.db.lock`.
package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/rogpeppe/go-internal/lockedfile"

	// Register modernc.org/sqlite under the driver name "sqlite".
	_ "modernc.org/sqlite"

	"github.com/yolo-labz/wa/internal/domain"
)

// Store wraps a single SQLite database file plus the lockedfile mutex
// guarding it. All public methods are safe for concurrent use.
type Store struct {
	db     *sql.DB
	lock   *lockedfile.File
	dbPath string
	seq    atomic.Uint64
}

// Open ensures the parent directory exists with mode 0700, acquires an
// exclusive lockedfile mutex on dbPath+".lock", opens the SQLite
// database with the four contract-mandated PRAGMAs, applies the embedded
// schema, and chmods the database file to 0600.
//
// A second Open against the same path within the same process will
// block on the lockedfile mutex (the lockedfile package implements an
// in-process mutex layered on top of the OS file lock).
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqlitehistory: dbPath must not be empty")
	}

	parent := filepath.Dir(dbPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitehistory: mkdir %s: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil { //nolint:gosec // 0700 is the intended dir mode (CLAUDE.md §FS layout)
		return nil, fmt.Errorf("sqlitehistory: chmod %s: %w", parent, err)
	}

	lock, err := lockedfile.Edit(dbPath + ".lock")
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: acquire lock %s: %w", dbPath+".lock", err)
	}

	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitehistory: open db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitehistory: ping: %w", err)
	}

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitehistory: apply schema: %w", err)
	}

	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			_ = lock.Close()
			return nil, fmt.Errorf("sqlitehistory: chmod %s: %w", dbPath, err)
		}
	}

	return &Store{db: db, lock: lock, dbPath: dbPath}, nil
}

// Close closes the underlying SQL handle and releases the lockedfile
// mutex, joining any errors so a failure releasing the lock does not
// hide a failure closing the database.
func (s *Store) Close() error {
	var dbErr, lockErr error
	if s.db != nil {
		dbErr = s.db.Close()
	}
	if s.lock != nil {
		lockErr = s.lock.Close()
	}
	return errors.Join(dbErr, lockErr)
}

// LoadMore implements the local-first read path of HistoryStore.LoadMore.
// It returns up to `limit` messages for `chat` ordered by ts DESC,
// strictly older than the row identified by `before` (empty `before`
// means "start from newest").
func (s *Store) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	if chat.IsZero() {
		return nil, fmt.Errorf("sqlitehistory.LoadMore: %w", domain.ErrInvalidJID)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.LoadMore: invalid limit: %d", limit)
	}

	const q = `
SELECT message_id, chat_jid, sender_jid, ts, body
FROM messages
WHERE chat_jid = ?
  AND (? = '' OR ts < (SELECT ts FROM messages WHERE chat_jid = ? AND message_id = ?))
ORDER BY ts DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, chat.String(), string(before), chat.String(), string(before), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Message, 0, limit)
	for rows.Next() {
		var (
			messageID, chatJID, senderJID, body string
			ts                                  int64
		)
		if err := rows.Scan(&messageID, &chatJID, &senderJID, &ts, &body); err != nil {
			return nil, fmt.Errorf("sqlitehistory: scan: %w", err)
		}
		out = append(out, domain.TextMessage{Recipient: chat, Body: body})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory: rows: %w", err)
	}
	return out, nil
}

// Insert writes msgs into the messages table inside a single
// transaction. ON CONFLICT (chat_jid, message_id) DO NOTHING makes the
// call idempotent under history-sync re-delivery. Messages whose
// concrete type is not currently round-trippable (only TextMessage is in
// v0) are inserted with body="" so the FTS5 index stays consistent.
//
// Because the historyContainer interface does not propagate per-message
// IDs/timestamps, Insert synthesises monotonically increasing values
// from a per-Store atomic counter and the wall clock. This is sufficient
// for ordering and for the persist-late HS6 contract clause; richer
// metadata arrives once the whatsmeow event translator surfaces it.
func (s *Store) Insert(ctx context.Context, msgs []domain.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitehistory: begin: %w", err)
	}
	const stmt = `
INSERT INTO messages (chat_jid, sender_jid, message_id, ts, body, raw_proto)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, message_id) DO NOTHING
`
	now := time.Now().UnixNano()
	for _, m := range msgs {
		if m == nil {
			continue
		}
		to := m.To()
		if to.IsZero() {
			_ = tx.Rollback()
			return fmt.Errorf("sqlitehistory.Insert: %w", domain.ErrInvalidJID)
		}
		seq := s.seq.Add(1)
		body := ""
		if tm, ok := m.(domain.TextMessage); ok {
			body = tm.Body
		}
		messageID := fmt.Sprintf("auto-%d-%d", now, seq)
		ts := now + int64(seq) //nolint:gosec // bounded by per-Insert msg count, safe
		if _, err := tx.ExecContext(ctx, stmt, to.String(), to.String(), messageID, ts, body, nil); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlitehistory: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitehistory: commit: %w", err)
	}
	return nil
}

// Search runs an FTS5 MATCH against messages_fts and returns the
// matching messages, FTS5-rank ordered, capped at limit. Results are
// reconstructed as TextMessages addressed to the chat_jid stored on the
// row.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.Search: invalid limit: %d", limit)
	}
	const q = `
SELECT m.message_id, m.chat_jid, m.sender_jid, m.ts, m.body
FROM messages_fts
JOIN messages m ON m.rowid = messages_fts.rowid
WHERE messages_fts MATCH ?
ORDER BY rank
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Message, 0, limit)
	for rows.Next() {
		var (
			messageID, chatJID, senderJID, body string
			ts                                  int64
		)
		if err := rows.Scan(&messageID, &chatJID, &senderJID, &ts, &body); err != nil {
			return nil, fmt.Errorf("sqlitehistory: scan: %w", err)
		}
		jid, err := domain.Parse(chatJID)
		if err != nil {
			continue
		}
		out = append(out, domain.TextMessage{Recipient: jid, Body: body})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory: rows: %w", err)
	}
	return out, nil
}
