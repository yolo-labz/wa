package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// session_meta carries two independent instants, and both are stored the
// same way: one INTEGER column on the singleton row, 0 meaning "unknown".
// readEpoch/writeEpoch hold that shared mechanism so PairedAt and
// WarmupEpoch differ only in the column they name and the meaning they
// document — which is the only thing that actually differs between them.
//
// The SQL is written out per column rather than assembled by concatenating
// a column name into a query string. Two static literals are greppable,
// need no gosec exemption, and cannot become an injection the day someone
// makes the column name a parameter.
type epochColumn struct {
	name    string
	selectQ string
	upsertQ string
}

var (
	pairedAtColumn = epochColumn{
		name:    "paired_at",
		selectQ: `SELECT paired_at FROM session_meta WHERE id = 1`,
		upsertQ: `INSERT INTO session_meta (id, is_business, updated_at, paired_at) VALUES (1, 0, ?, ?)
		          ON CONFLICT(id) DO UPDATE SET paired_at  = excluded.paired_at,
		                                        updated_at = excluded.updated_at`,
	}
	warmupEpochColumn = epochColumn{
		name:    "warmup_epoch",
		selectQ: `SELECT warmup_epoch FROM session_meta WHERE id = 1`,
		upsertQ: `INSERT INTO session_meta (id, is_business, updated_at, warmup_epoch) VALUES (1, 0, ?, ?)
		          ON CONFLICT(id) DO UPDATE SET warmup_epoch = excluded.warmup_epoch,
		                                        updated_at   = excluded.updated_at`,
	}
)

// readEpoch returns the instant recorded in col, and false when the row is
// absent or the value is 0 ("unknown" — the honest answer for a session
// that predates the column).
func (s *Store) readEpoch(ctx context.Context, col epochColumn) (time.Time, bool, error) {
	if s == nil || s.db == nil {
		return time.Time{}, false, fmt.Errorf("sqlitestore: read %s on a closed store", col.name)
	}
	if err := EnsureSessionMetaSchema(ctx, s.db); err != nil {
		return time.Time{}, false, err
	}
	var epoch int64
	err := s.db.QueryRowContext(ctx, col.selectQ).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("sqlitestore: read %s: %w", col.name, err)
	}
	if epoch <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(epoch, 0).UTC(), true, nil
}

// writeEpoch records t in col, replacing any earlier value. A zero t
// clears the record back to unknown.
//
// updated_at is NOT NULL with no default, so the insert branch has to
// supply it; it tracks the last write to the row, not the instant in col.
func (s *Store) writeEpoch(ctx context.Context, col epochColumn, t time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlitestore: set %s on a closed store", col.name)
	}
	if err := EnsureSessionMetaSchema(ctx, s.db); err != nil {
		return err
	}
	var epoch int64
	if !t.IsZero() {
		epoch = t.Unix()
	}
	if _, err := s.db.ExecContext(ctx, col.upsertQ, time.Now().Unix(), epoch); err != nil {
		return fmt.Errorf("sqlitestore: set %s: %w", col.name, err)
	}
	return nil
}
