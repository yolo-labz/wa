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
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-64000)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=mmap_size(268435456)" +
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitehistory: open db: %w", err)
	}
	// Single-writer daemon: one connection prevents intra-process
	// SQLITE_BUSY contention that busy_timeout does not resolve.
	// Feature 009 — spec FR-029.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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

	// Apply pending migrations (v1→v2 adds media_type, caption, etc.).
	if err := migrateIfNeeded(ctx, db); err != nil {
		_ = db.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("sqlitehistory: migrate: %w", err)
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
SELECT message_id, chat_jid, sender_jid, ts, body, media_type
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
			messageID, chatJID, senderJID, body, mediaType string
			ts                                             int64
		)
		if err := rows.Scan(&messageID, &chatJID, &senderJID, &ts, &body, &mediaType); err != nil {
			return nil, fmt.Errorf("sqlitehistory: scan: %w", err)
		}
		if mediaType != "" {
			out = append(out, domain.MediaMessage{Recipient: chat, Path: messageID, Mime: mediaType, Caption: body})
		} else {
			out = append(out, domain.TextMessage{Recipient: chat, Body: body})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory: rows: %w", err)
	}
	return out, nil
}

// Insert writes msgs into the messages table inside a single
// transaction using a prepared statement. ON CONFLICT (chat_jid,
// message_id) DO NOTHING makes the call idempotent under history-sync
// re-delivery. Message bodies are sanitized before storage (FR-038).
//
// Feature 009 rewrote this to accept []StoredMessage with real
// WhatsApp metadata instead of synthesized placeholders.
// Spec FR-003, FR-005, FR-022, FR-025, FR-030.
func (s *Store) Insert(ctx context.Context, msgs []StoredMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitehistory: begin: %w", err)
	}

	const insertSQL = `
INSERT INTO messages (chat_jid, sender_jid, message_id, ts, body, media_type, caption, is_from_me, push_name, raw_proto)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, message_id) DO NOTHING
`
	prepared, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlitehistory: prepare: %w", err)
	}
	defer func() { _ = prepared.Close() }()

	for _, m := range msgs {
		if m.ChatJID == "" || m.MessageID == "" {
			continue
		}
		body := SanitizeBody(m.Body)
		caption := SanitizeBody(m.Caption)
		isFromMe := 0
		if m.IsFromMe {
			isFromMe = 1
		}
		if _, err := prepared.ExecContext(ctx,
			m.ChatJID, m.SenderJID, m.MessageID, m.Timestamp,
			body, m.MediaType, caption, isFromMe, m.PushName, m.RawProto,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlitehistory: insert %s/%s: %w", m.ChatJID, m.MessageID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitehistory: commit: %w", err)
	}
	return nil
}

// InsertDomainMessages is the legacy Insert path for the HistoryStore
// port contract (HS6 persist-late clause). It wraps []domain.Message
// into []StoredMessage with auto-generated IDs for backward compat.
func (s *Store) InsertDomainMessages(ctx context.Context, msgs []domain.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	now := time.Now().Unix()
	stored := make([]StoredMessage, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		seq := s.seq.Add(1)
		to := m.To()
		if to.IsZero() {
			continue
		}
		body := ""
		if tm, ok := m.(domain.TextMessage); ok {
			body = tm.Body
		}
		stored = append(stored, StoredMessage{
			ChatJID:   to.String(),
			SenderJID: to.String(),
			MessageID: fmt.Sprintf("auto-%d-%d", now, seq),
			Timestamp: now + int64(seq), //nolint:gosec // bounded by per-Insert msg count
			Body:      body,
		})
	}
	return s.Insert(ctx, stored)
}

// InsertRaw persists a single message with explicit metadata fields.
// This is the bridge method that the whatsmeow adapter calls from
// handleWAEvent without needing to import sqlitehistory types.
// The 10-param signature bridges two adapter packages that deliberately
// do not share types (hexagonal boundary). SonarQube go:S107 accepted.
// Feature 009 — spec FR-001.
func (s *Store) InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool) error { //nolint:revive // param count is the hexagonal boundary bridge  //NOSONAR go:S107 — bridges two adapter packages that do not share types
	return s.Insert(ctx, []StoredMessage{{
		ChatJID:   chatJID,
		SenderJID: senderJID,
		MessageID: messageID,
		Timestamp: ts,
		Body:      body,
		MediaType: mediaType,
		Caption:   caption,
		PushName:  pushName,
		IsFromMe:  isFromMe,
	}})
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
