package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PairedAt returns the instant this device completed its pairing
// handshake, and false when that instant is unknown.
//
// Unknown is the honest answer for every session paired before issue
// #311 landed: whatsmeow's own schema has no pairing timestamp (the
// whatsmeow_device row carries keys, signatures and lid_migration_ts,
// nothing else time-shaped), and SQLite does not record when a row was
// inserted. There is nothing to back-fill from, so callers report the
// field as absent rather than substituting a number that reads as
// evidence — which is the bug #311 describes.
func (s *Store) PairedAt(ctx context.Context) (time.Time, bool, error) {
	if s == nil || s.db == nil {
		return time.Time{}, false, errors.New("sqlitestore: PairedAt on a closed store")
	}
	if err := EnsureSessionMetaSchema(ctx, s.db); err != nil {
		return time.Time{}, false, err
	}
	var epoch int64
	err := s.db.QueryRowContext(ctx, `SELECT paired_at FROM session_meta WHERE id = 1`).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("sqlitestore: read paired_at: %w", err)
	}
	if epoch <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(epoch, 0).UTC(), true, nil
}

// SetPairedAt records the pairing instant, replacing any earlier value —
// a re-pair starts a new session, so the newest handshake is the one the
// timestamp describes. A zero t clears the record back to unknown.
//
// updated_at is NOT NULL with no default, so the insert branch has to
// supply it; it tracks the last write to the row, not the pairing.
func (s *Store) SetPairedAt(ctx context.Context, t time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("sqlitestore: SetPairedAt on a closed store")
	}
	if err := EnsureSessionMetaSchema(ctx, s.db); err != nil {
		return err
	}
	var epoch int64
	if !t.IsZero() {
		epoch = t.Unix()
	}
	const q = `
		INSERT INTO session_meta (id, is_business, updated_at, paired_at) VALUES (1, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET paired_at  = excluded.paired_at,
		                              updated_at = excluded.updated_at
	`
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, q, now, epoch); err != nil {
		return fmt.Errorf("sqlitestore: set paired_at: %w", err)
	}
	return nil
}
