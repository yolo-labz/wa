package sqlitestore

import (
	"context"
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
	return s.readEpoch(ctx, pairedAtColumn)
}

// SetPairedAt records the pairing instant, replacing any earlier value —
// a re-pair starts a new session, so the newest handshake is the one the
// timestamp describes. A zero t clears the record back to unknown.
//
// updated_at is NOT NULL with no default, so the insert branch has to
// supply it; it tracks the last write to the row, not the pairing.
func (s *Store) SetPairedAt(ctx context.Context, t time.Time) error {
	return s.writeEpoch(ctx, pairedAtColumn, t)
}
