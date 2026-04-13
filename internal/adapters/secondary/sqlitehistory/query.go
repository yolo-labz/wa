package sqlitehistory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// QueryHistory returns StoredMessages for a specific chat, newest-first,
// with cursor-based pagination via `before` (message ID). Empty `before`
// means start from newest. Feature 009 — spec FR-014, FR-023.
func (s *Store) QueryHistory(ctx context.Context, chatJID string, before string, limit int) ([]StoredMessage, error) {
	if chatJID == "" {
		return nil, fmt.Errorf("sqlitehistory.QueryHistory: empty chat JID")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.QueryHistory: invalid limit: %d", limit)
	}
	if limit > 200 {
		limit = 200
	}

	const q = `
SELECT message_id, chat_jid, sender_jid, ts, body, media_type, caption, is_from_me, push_name
FROM messages
WHERE chat_jid = ?
  AND (? = '' OR ts < (SELECT ts FROM messages WHERE chat_jid = ? AND message_id = ?))
ORDER BY ts DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, chatJID, before, chatJID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: query history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanStoredMessages(rows, limit)
}

// QueryMessages returns StoredMessages across all chats, newest-first.
// Feature 009 — spec FR-015.
func (s *Store) QueryMessages(ctx context.Context, limit int) ([]StoredMessage, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.QueryMessages: invalid limit: %d", limit)
	}
	if limit > 200 {
		limit = 200
	}

	const q = `
SELECT message_id, chat_jid, sender_jid, ts, body, media_type, caption, is_from_me, push_name
FROM messages
ORDER BY ts DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanStoredMessages(rows, limit)
}

// QuerySearch returns StoredMessages matching an FTS5 query, ordered by
// relevance rank. Feature 009 — spec FR-016.
func (s *Store) QuerySearch(ctx context.Context, query string, limit int) ([]StoredMessage, error) {
	if query == "" {
		return nil, fmt.Errorf("sqlitehistory.QuerySearch: empty query")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.QuerySearch: invalid limit: %d", limit)
	}
	if limit > 100 {
		limit = 100
	}

	const q = `
SELECT m.message_id, m.chat_jid, m.sender_jid, m.ts, m.body, m.media_type, m.caption, m.is_from_me, m.push_name
FROM messages_fts
JOIN messages m ON m.rowid = messages_fts.rowid
WHERE messages_fts MATCH ?
ORDER BY rank
LIMIT ?
`
	// FTS5 MATCH has its own syntax (AND, OR, NOT, *, NEAR, column
	// filters). Quote the user input to prevent syntax abuse.
	safeQuery := "\"" + strings.ReplaceAll(query, "\"", "\"\"") + "\""
	rows, err := s.db.QueryContext(ctx, q, safeQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanStoredMessages(rows, limit)
}

// PurgeChat deletes all messages for a given chat JID from the messages
// table. The FTS5 DELETE triggers handle index cleanup automatically.
// Returns the number of rows deleted. Feature 009 — spec FR-033.
func (s *Store) PurgeChat(ctx context.Context, chatJID string) (int64, error) {
	if chatJID == "" {
		return 0, fmt.Errorf("sqlitehistory.PurgeChat: empty chat JID")
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM messages WHERE chat_jid = ?", chatJID)
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: purge %s: %w", chatJID, err)
	}
	return res.RowsAffected()
}

// CleanupRetention deletes messages older than the given duration.
// Called on startup and hourly when retention_days > 0.
// Feature 009 — spec FR-035.
func (s *Store) CleanupRetention(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	res, err := s.db.ExecContext(ctx, "DELETE FROM messages WHERE ts < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: retention cleanup: %w", err)
	}
	return res.RowsAffected()
}

// ExportChat returns all StoredMessages for a chat, oldest-first.
// Feature 009 — spec FR-036.
func (s *Store) ExportChat(ctx context.Context, chatJID string) ([]StoredMessage, error) {
	if chatJID == "" {
		return nil, fmt.Errorf("sqlitehistory.ExportChat: empty chat JID")
	}

	const maxExportRows = 100_000
	const q = `
SELECT message_id, chat_jid, sender_jid, ts, body, media_type, caption, is_from_me, push_name
FROM messages
WHERE chat_jid = ?
ORDER BY ts ASC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, chatJID, maxExportRows)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: export: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanStoredMessages(rows, 0) // 0 = no cap
}

// rowScanner abstracts *sql.Rows for scanStoredMessages.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// scanStoredMessages scans rows into a []StoredMessage slice.
// If cap > 0, pre-allocates with that capacity hint.
func scanStoredMessages(rows rowScanner, cap int) ([]StoredMessage, error) {
	if cap <= 0 {
		cap = 64
	}
	out := make([]StoredMessage, 0, cap)
	for rows.Next() {
		var (
			m        StoredMessage
			isFromMe int
		)
		if err := rows.Scan(
			&m.MessageID, &m.ChatJID, &m.SenderJID, &m.Timestamp,
			&m.Body, &m.MediaType, &m.Caption, &isFromMe, &m.PushName,
		); err != nil {
			return nil, fmt.Errorf("sqlitehistory: scan: %w", err)
		}
		m.IsFromMe = isFromMe != 0
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory: rows: %w", err)
	}
	return out, nil
}
