package sqlitehistory

import (
	"context"
	"fmt"
)

// ContentDecoder re-projects a stored raw_proto onto (body, media_type,
// caption). The whatsmeow adapter supplies it (whatsmeow.ContentFromRaw);
// this package cannot import that adapter (hexagonal boundary), so the
// decode crosses as a function value.
type ContentDecoder func(raw []byte) (body, mediaType, caption string)

const rehydrateSQL = `UPDATE messages
SET body = ?, media_type = ?, caption = ?, caption_folded = ? WHERE rowid = ? AND body = ''`

// RehydrateEmptyBodies recovers rows persisted with nothing renderable
// while their raw_proto was kept — every message of a variant the decoder
// did not yet understand (h151: WhatsApp Business templates, interactive,
// buttons and list messages stored body=""). Each such row is decoded
// again and updated only when the decoder now yields content, so the pass
// is idempotent and safe to run at every start: a row that still decodes
// to nothing is left untouched. Revoked rows stay empty on purpose. The
// messages_au trigger re-indexes the new body into FTS.
//
// Rows are read fully before writing, for the reason migrate_v8 gives:
// an open SELECT cursor plus an UPDATE on one connection is what SQLite
// answers with "database table is locked".
func (s *Store) RehydrateEmptyBodies(ctx context.Context, decode ContentDecoder) (int, error) {
	type update struct {
		rowid                    int64
		body, mediaType, caption string
	}
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, raw_proto FROM messages
WHERE body = '' AND COALESCE(media_type, '') = '' AND raw_proto IS NOT NULL AND revoked_at IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: rehydrate scan: %w", err)
	}
	var pending []update
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			closeRows(rows, "RehydrateEmptyBodies")
			return 0, fmt.Errorf("sqlitehistory: rehydrate scan row: %w", err)
		}
		body, mediaType, caption := decode(raw)
		if body = SanitizeBody(body); body != "" || mediaType != "" {
			pending = append(pending, update{id, body, mediaType, SanitizeBody(caption)})
		}
	}
	if err := rows.Err(); err != nil {
		closeRows(rows, "RehydrateEmptyBodies")
		return 0, fmt.Errorf("sqlitehistory: rehydrate rows: %w", err)
	}
	closeRows(rows, "RehydrateEmptyBodies")
	if len(pending) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: rehydrate begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, u := range pending {
		if _, err := tx.ExecContext(ctx, rehydrateSQL, u.body, u.mediaType, u.caption, foldText(u.caption), u.rowid); err != nil {
			return 0, fmt.Errorf("sqlitehistory: rehydrate rowid %d: %w", u.rowid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlitehistory: rehydrate commit: %w", err)
	}
	return len(pending), nil
}
