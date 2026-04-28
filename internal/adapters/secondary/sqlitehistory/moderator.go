package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// GetMessageMeta returns the timestamp and body of the message identified
// by (chat, id). Used by the whatsmeow MessageModerator adapter to gate
// Edit against the 15-minute window (feature 018 FR-015a) and to
// preserve the original body into previous_body before overwriting.
//
// Returns a wrapped os.ErrNotExist when no row matches so callers can
// errors.Is the category.
func (s *Store) GetMessageMeta(ctx context.Context, chat domain.JID, id domain.MessageID) (ts int64, body string, err error) {
	if chat.IsZero() {
		return 0, "", fmt.Errorf("sqlitehistory.GetMessageMeta: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return 0, "", errors.New("sqlitehistory.GetMessageMeta: empty messageID")
	}
	const q = `SELECT ts, body FROM messages WHERE chat_jid = ? AND message_id = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, chat.String(), string(id))
	if err := row.Scan(&ts, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", fmt.Errorf("sqlitehistory.GetMessageMeta: %s/%s: %w", chat, id, os.ErrNotExist)
		}
		return 0, "", fmt.Errorf("sqlitehistory.GetMessageMeta: scan: %w", err)
	}
	return ts, body, nil
}

// StampRevoke marks the message identified by (chat, id) as revoked by
// writing revoked_at on the row. The scope is recorded as a signed-bit
// flag on revoked_at itself: positive = scope=everyone, negative =
// scope=self. This keeps the v4 schema compact (one column, one index)
// while preserving the distinction the audit path needs.
//
// No-op for rows that do not exist (Revoke self must not be an error when
// the original message row is absent from the local store, e.g. after a
// fresh install). Feature 018 FR-014.
func (s *Store) StampRevoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope, revokedAt int64) error {
	if chat.IsZero() {
		return fmt.Errorf("sqlitehistory.StampRevoke: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("sqlitehistory.StampRevoke: empty messageID")
	}
	if !scope.IsValid() {
		return fmt.Errorf("sqlitehistory.StampRevoke: invalid scope %d", uint8(scope))
	}
	stamp := revokedAt
	if scope == domain.RevokeSelf {
		stamp = -stamp
	}
	const q = `UPDATE messages SET revoked_at = ? WHERE chat_jid = ? AND message_id = ?`
	if _, err := s.db.ExecContext(ctx, q, stamp, chat.String(), string(id)); err != nil {
		return fmt.Errorf("sqlitehistory.StampRevoke: %w", err)
	}
	return nil
}

// StampEdit overwrites the body of the message identified by (chat, id),
// preserving the original body in previous_body and recording editedAt.
// The caller MUST have already verified the 15-minute window. Returns a
// wrapped os.ErrNotExist if no row matches. Feature 018 FR-015.
func (s *Store) StampEdit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string, editedAt int64) error {
	if chat.IsZero() {
		return fmt.Errorf("sqlitehistory.StampEdit: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("sqlitehistory.StampEdit: empty messageID")
	}
	const q = `
UPDATE messages
SET previous_body = body,
    body          = ?,
    edited_at     = ?
WHERE chat_jid = ? AND message_id = ?
`
	res, err := s.db.ExecContext(ctx, q, SanitizeBody(newBody), editedAt, chat.String(), string(id))
	if err != nil {
		return fmt.Errorf("sqlitehistory.StampEdit: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlitehistory.StampEdit: rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlitehistory.StampEdit: %s/%s: %w", chat, id, os.ErrNotExist)
	}
	return nil
}
