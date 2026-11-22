package sqlitehistory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// searchBM25Query ranks messages_fts (body only) hits by BM25. Chat and the
// time bounds use the 0-sentinel toggle idiom of ExportChatFiltered, so the
// statement stays static. A revoked row (any non-zero revoked_at, for everyone or for
// me, as storedToWire decides it) is never a hit: its text is gone for every reader, and a
// snippet would hand it back. snippet() gets no highlight markers, so the
// untrusted text reaches the channel envelope unaltered.
const searchBM25Query = `
SELECT m.message_id, m.chat_jid, m.sender_jid, m.ts,
       snippet(messages_fts, 0, '', '', '…', 32), bm25(messages_fts)
FROM messages_fts
JOIN messages m ON m.rowid = messages_fts.rowid
WHERE messages_fts MATCH ?
  AND (? = '' OR m.chat_jid = ?)
  AND (? = 0 OR m.ts >= ?)
  AND (? = 0 OR m.ts <= ?)
  AND COALESCE(m.revoked_at, 0) = 0
ORDER BY rank
LIMIT ?`

// SearchBM25 implements app.MessageSearcher: messages.search and the MCP
// wa_search_messages. Since and Until are inclusive Unix seconds, 0 = open.
func (s *Store) SearchBM25(ctx context.Context, q app.SearchQuery, limit int) ([]app.SearchHit, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitehistory.SearchBM25: invalid limit: %d", limit)
	}
	match := ftsMatch(q)
	if match == "" {
		return nil, errors.New("sqlitehistory.SearchBM25: empty query")
	}
	chat := ""
	if !q.Chat.IsZero() {
		chat = q.Chat.String()
	}
	rows, err := s.db.QueryContext(ctx, searchBM25Query, match, chat, chat, q.Since, q.Since, q.Until, q.Until, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitehistory.SearchBM25: %w", err)
	}
	defer closeRows(rows, "SearchBM25")
	hits := make([]app.SearchHit, 0, min(limit, 100))
	for rows.Next() {
		var (
			id, chatJID, senderJID, snippet string
			ts                              int64
			bm25                            float64
		)
		if err := rows.Scan(&id, &chatJID, &senderJID, &ts, &snippet, &bm25); err != nil {
			return nil, fmt.Errorf("sqlitehistory.SearchBM25: scan: %w", err)
		}
		c, err := domain.Parse(chatJID)
		if err != nil {
			continue // as Search: a row whose chat does not parse cannot be addressed
		}
		sender, _ := domain.Parse(senderJID) // zero when unparsable: the hit stays, unattributed
		hits = append(hits, app.SearchHit{
			MessageID: domain.MessageID(id), Chat: c, Sender: sender,
			Snippet: snippet, TS: time.Unix(ts, 0), Score: -bm25, // FTS5 bm25: lower is better
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitehistory.SearchBM25: rows: %w", err)
	}
	return hits, nil
}

// ftsMatch turns a structured query into an FTS5 expression. Every term
// becomes an FTS5 string ("…" with quotes doubled), so caller text is never
// FTS5 syntax: the phrase is one phrase, All terms are ANDed and Any terms
// form one OR group.
func ftsMatch(q app.SearchQuery) string {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	var parts, anyOf []string
	for _, t := range append([]string{q.Phrase}, q.All...) {
		if strings.TrimSpace(t) != "" {
			parts = append(parts, quote(t))
		}
	}
	for _, t := range q.Any {
		if strings.TrimSpace(t) != "" {
			anyOf = append(anyOf, quote(t))
		}
	}
	if len(anyOf) > 0 {
		parts = append(parts, "("+strings.Join(anyOf, " OR ")+")")
	}
	return strings.Join(parts, " AND ")
}
