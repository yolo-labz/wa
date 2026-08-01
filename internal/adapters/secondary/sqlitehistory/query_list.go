package sqlitehistory

import (
	"context"
	"fmt"
	"strings"
)

// ChatSummary is one row of QueryChats: an aggregate over the messages
// table for a single chat. There is no chats table (only messages), so
// last-activity, count, and a representative push_name are derived by
// grouping. Issue #173 — `wa chat list`, `wa chat last-active`.
type ChatSummary struct {
	ChatJID       string
	PushName      string // most-recent non-empty push_name seen in the chat
	LastMessageTS int64
	MessageCount  int64
	IsGroup       bool
}

// QueryChats returns one ChatSummary per chat, most-recently-active first.
// limit <= 0 defaults to 200; capped at 1000. The representative push_name
// is the newest non-empty one in the chat (a hint for DMs; for groups it is
// the last sender's name, so callers should lean on IsGroup). Issue #173.
func (s *Store) QueryChats(ctx context.Context, limit int) ([]ChatSummary, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	// idx_messages_chat_ts (chat_jid, ts DESC) makes both the GROUP BY and
	// the correlated push_name lookup index-only seeks.
	const q = `
SELECT m.chat_jid,
       MAX(m.ts) AS last_ts,
       COUNT(*)  AS cnt,
       COALESCE((SELECT push_name FROM messages
                 WHERE chat_jid = m.chat_jid AND COALESCE(push_name, '') != ''
                 ORDER BY ts DESC LIMIT 1), '') AS push_name
FROM messages m
GROUP BY m.chat_jid
ORDER BY last_ts DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: query chats: %w", err)
	}
	defer closeRows(rows, "QueryChats")

	out := make([]ChatSummary, 0, limit)
	for rows.Next() {
		var c ChatSummary
		if err := rows.Scan(&c.ChatJID, &c.LastMessageTS, &c.MessageCount, &c.PushName); err != nil {
			return nil, fmt.Errorf("sqlitehistory: scan chat: %w", err)
		}
		c.IsGroup = strings.HasSuffix(c.ChatJID, "@g.us")
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory: chats rows: %w", err)
	}
	return out, nil
}

// MessageFilter parameterises QueryMessagesFiltered. Zero-value fields mean
// "no filter on this dimension". Issue #173 — `wa messages list`.
type MessageFilter struct {
	ChatJID       string // "" = all chats
	SenderJID     string // "" = any sender; matches either JID namespace
	MediaTypeLike string // SQL LIKE pattern, e.g. "audio/%"; "" = any (incl. text)
	CaptionLike   string // SQL LIKE pattern (ESCAPE '\'), e.g. `%catalog%`; "" = any
	FromMe        *bool  // nil = either direction
	Since         int64  // unix seconds, 0 = no lower bound
	Until         int64  // unix seconds, 0 = no upper bound
	Limit         int    // <= 0 → 50, capped at 500
}

// QueryMessagesFiltered returns StoredMessages matching a filter,
// newest-first. Server-side filtering lets callers find "the 3 most recent
// audios in this chat" without dumping the whole chat as NDJSON. Issue #173.
func (s *Store) QueryMessagesFiltered(ctx context.Context, f MessageFilter) ([]StoredMessage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var (
		where []string
		args  []any
	)
	if f.ChatJID != "" {
		where = append(where, "chat_jid = ?")
		args = append(args, f.ChatJID)
	}
	if f.SenderJID != "" {
		// Match BOTH namespaces. A row records its sender under whichever
		// addressing mode the chat uses, with the other one in
		// sender_alt_jid (spec 107). Filtering on sender_jid alone silently
		// drops half a person's messages in a LID-addressed group — and a
		// sender filter that quietly under-matches is worse than none,
		// because it reads as "this person sent nothing here".
		where = append(where, "(sender_jid = ? OR sender_alt_jid = ?)")
		args = append(args, f.SenderJID, f.SenderJID)
	}
	if f.MediaTypeLike != "" {
		where = append(where, "media_type LIKE ?")
		args = append(args, f.MediaTypeLike)
	}
	if f.CaptionLike != "" {
		// LIKE, not FTS5: messages_fts indexes `body` only, so a caption
		// keyword is unreachable through Search. Captions are short and the
		// scan is already bounded by the other predicates plus LIMIT.
		where = append(where, `caption LIKE ? ESCAPE '\'`)
		args = append(args, f.CaptionLike)
	}
	if f.FromMe != nil {
		v := 0
		if *f.FromMe {
			v = 1
		}
		where = append(where, "is_from_me = ?")
		args = append(args, v)
	}
	if f.Since > 0 {
		where = append(where, "ts >= ?")
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		where = append(where, "ts <= ?")
		args = append(args, f.Until)
	}

	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	q := `
SELECT message_id, chat_jid, sender_jid, ts, body, media_type, caption, is_from_me, push_name,
       COALESCE(sender_alt_jid, ''), COALESCE(addressing_mode, ''), interactive_json
FROM messages
` + clause + `
ORDER BY ts DESC
LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory: query messages filtered: %w", err)
	}
	defer closeRows(rows, "QueryMessagesFiltered")

	return scanStoredMessages(rows, limit)
}
