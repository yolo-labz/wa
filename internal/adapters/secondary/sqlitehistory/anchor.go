package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// NewestRef returns a reference to the newest stored message in chat — the
// anchor for a gap-recovery on-demand history pull (whatsmeow fetches
// `count` messages BEFORE the anchor, so anchoring at the newest known
// message re-pulls the most recent slice and fills any window missed during
// a stall; the store dedups the overlap). ok=false when the chat has no
// stored messages. Spec 110g / PR #222.
func (s *Store) NewestRef(ctx context.Context, chat domain.JID) (domain.MessageRef, bool, error) {
	return s.refByOrder(ctx, chat, false)
}

// OldestRef returns a reference to the oldest stored message in chat — the
// anchor for scroll-up pagination (fetch `count` messages older than the
// oldest known). ok=false when the chat has no stored messages. PR #222.
func (s *Store) OldestRef(ctx context.Context, chat domain.JID) (domain.MessageRef, bool, error) {
	return s.refByOrder(ctx, chat, true)
}

// refByOrder reads a single message reference for chat, ordered by timestamp
// ascending (oldest) or descending (newest). message_id is the tie-breaker
// so the result is deterministic when two rows share a timestamp.
func (s *Store) refByOrder(ctx context.Context, chat domain.JID, ascending bool) (domain.MessageRef, bool, error) {
	order := "DESC"
	if ascending {
		order = "ASC"
	}
	// `order` is a fixed in-code literal ("ASC"/"DESC"), never user input —
	// SQL cannot parameterise an ORDER BY direction. chat_jid stays a bound
	// parameter.
	q := "SELECT message_id, ts, is_from_me FROM messages WHERE chat_jid = ? ORDER BY ts " + order + ", message_id " + order + " LIMIT 1"

	var (
		id       string
		ts       int64
		isFromMe int // SQLite stores the bool as INTEGER 0/1; scan as int.
	)
	err := s.db.QueryRowContext(ctx, q, chat.String()).Scan(&id, &ts, &isFromMe)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MessageRef{}, false, nil
	}
	if err != nil {
		return domain.MessageRef{}, false, fmt.Errorf("sqlitehistory: %s ref for %s: %w", order, chat, err)
	}
	return domain.MessageRef{
		Chat:      chat,
		ID:        domain.MessageID(id),
		FromMe:    isFromMe != 0,
		Timestamp: time.Unix(ts, 0),
	}, true, nil
}

// RecentChats returns up to limit chat JIDs, most-recently-active first —
// the set a global gap-recovery sweep anchors against (one pull per chat,
// since whatsmeow's on-demand request is per-chat). Unparseable stored JIDs
// are skipped rather than failing the whole sweep. PR #222.
func (s *Store) RecentChats(ctx context.Context, limit int) ([]domain.JID, error) {
	summaries, err := s.QueryChats(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]domain.JID, 0, len(summaries))
	for _, c := range summaries {
		j, err := domain.Parse(c.ChatJID)
		if err != nil {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}
