package sqliteevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// ErrInvalidLimit is returned when Range is called with limit <= 0.
var ErrInvalidLimit = errors.New("sqliteevents: invalid limit")

// Append implements app.EventBuffer. Assigns a monotonic seq if rec.Seq ==
// 0; otherwise honours the caller-supplied seq (used by integration tests
// and replay). Drops oldest rows when count exceeds capacity; the drop
// counter persists in events_meta so restarts preserve FR-063 accounting.
func (s *Store) Append(ctx context.Context, rec app.EventRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.Kind == "" {
		return errors.New("sqliteevents: append: empty kind")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqliteevents: append tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	trusted := rec.TrustedJSON
	if len(trusted) == 0 {
		trusted = []byte(`{}`)
	}
	untrusted := rec.UntrustedJSON
	if untrusted == nil {
		untrusted = []byte{}
	}
	if rec.Seq == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (kind, trusted_json, untrusted_json, created_at) VALUES (?, ?, ?, ?)`,
			rec.Kind, trusted, untrusted, createdAt.Unix(),
		); err != nil {
			return fmt.Errorf("sqliteevents: insert: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (seq, kind, trusted_json, untrusted_json, created_at) VALUES (?, ?, ?, ?, ?)`,
			rec.Seq, rec.Kind, trusted, untrusted, createdAt.Unix(),
		); err != nil {
			return fmt.Errorf("sqliteevents: insert seq=%d: %w", rec.Seq, err)
		}
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		return fmt.Errorf("sqliteevents: count: %w", err)
	}
	if count > s.capacity {
		dropN := count - s.capacity
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM events WHERE seq IN (SELECT seq FROM events ORDER BY seq ASC LIMIT ?)`,
			dropN,
		); err != nil {
			return fmt.Errorf("sqliteevents: drop oldest: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE events_meta SET dropped_total = dropped_total + ? WHERE id = 1`,
			dropN,
		); err != nil {
			return fmt.Errorf("sqliteevents: bump dropped: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqliteevents: append commit: %w", err)
	}
	return nil
}

// Range implements app.EventBuffer.
func (s *Store) Range(ctx context.Context, sinceSeq int64, limit int) ([]app.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidLimit, limit)
	}
	const q = `
SELECT seq, kind, trusted_json, untrusted_json, created_at
FROM events
WHERE seq > ?
ORDER BY seq ASC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("sqliteevents: range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]app.EventRecord, 0, limit)
	for rows.Next() {
		var (
			seq, createdAt int64
			kind           string
			trusted, untr  []byte
		)
		if err := rows.Scan(&seq, &kind, &trusted, &untr, &createdAt); err != nil {
			return nil, fmt.Errorf("sqliteevents: range scan: %w", err)
		}
		out = append(out, app.EventRecord{
			Seq:           seq,
			Kind:          kind,
			TrustedJSON:   trusted,
			UntrustedJSON: untr,
			CreatedAt:     time.Unix(createdAt, 0),
		})
	}
	return out, rows.Err()
}

// Stats implements app.EventBuffer.
func (s *Store) Stats(ctx context.Context) (app.BufferStats, error) {
	if err := ctx.Err(); err != nil {
		return app.BufferStats{}, err
	}
	var (
		size           int
		oldest, newest sql.NullInt64
		dropped        int64
	)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(seq), MAX(seq) FROM events`).Scan(&size, &oldest, &newest); err != nil {
		return app.BufferStats{}, fmt.Errorf("sqliteevents: stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT dropped_total FROM events_meta WHERE id = 1`).Scan(&dropped); err != nil {
		return app.BufferStats{}, fmt.Errorf("sqliteevents: dropped: %w", err)
	}
	return app.BufferStats{
		Size:         size,
		Capacity:     s.capacity,
		OldestSeq:    oldest.Int64,
		NewestSeq:    newest.Int64,
		DroppedTotal: dropped,
	}, nil
}

// TrimTo implements app.EventBuffer.
func (s *Store) TrimTo(ctx context.Context, seq int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE seq <= ?`, seq)
	if err != nil {
		return 0, fmt.Errorf("sqliteevents: trim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqliteevents: trim rows: %w", err)
	}
	return int(n), nil
}

// SyntheticDrop builds a `stream.drop` EventRecord per FR-063 when a client
// resumes at sinceSeq and the oldest remaining row in the buffer is more
// than one seq ahead (i.e. the gap represents rows evicted by drop-oldest).
// Returns ok=false when no synthesis is warranted. Seq is left at 0 so the
// subscribe layer can assign it inline with live traffic.
func SyntheticDrop(sinceSeq, oldestSeq, dropped int64) (app.EventRecord, bool) {
	if oldestSeq == 0 || oldestSeq <= sinceSeq+1 {
		return app.EventRecord{}, false
	}
	payload := struct {
		Gap     int64 `json:"gap"`
		From    int64 `json:"from"`
		To      int64 `json:"to"`
		Dropped int64 `json:"dropped_total"`
	}{
		Gap:     oldestSeq - sinceSeq - 1,
		From:    sinceSeq + 1,
		To:      oldestSeq - 1,
		Dropped: dropped,
	}
	buf, _ := json.Marshal(payload)
	return app.EventRecord{
		Kind:        "stream.drop",
		TrustedJSON: buf,
		CreatedAt:   time.Now(),
	}, true
}
