package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// TranscriptStore is the sqlite-backed app.TranscriptStore implementation
// for spec 110h FR-004. Keyed on sha256 (content-addressed) so a voice
// note forwarded across N chats stores one row. Backed by the v6
// media_transcripts table.
//
// Safe for concurrent use by multiple goroutines; relies on sqlite's
// serialised writer + WAL readers.
type TranscriptStore struct {
	db    *sql.DB
	clock func() time.Time
}

// NewTranscriptStore constructs a TranscriptStore bound to db. Callers
// MUST ensure migrateIfNeeded has reached v6 before instantiating.
// Clock may be nil; in that case time.Now().UTC is used.
func NewTranscriptStore(db *sql.DB, clock func() time.Time) *TranscriptStore {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &TranscriptStore{db: db, clock: clock}
}

// TranscriptStore returns a TranscriptStore bound to this Store's
// database handle. The composition root uses this to wire the FR-004
// hydration path into the dispatcher without reaching into Store's
// private fields.
func (s *Store) TranscriptStore() *TranscriptStore {
	return NewTranscriptStore(s.db, nil)
}

// Upsert implements app.TranscriptStore. INSERT OR REPLACE on sha256
// PK — a second call with the same sha overwrites in place. CreatedAt
// is set to now when zero so callers can pass a freshly-built record
// without populating it.
func (s *TranscriptStore) Upsert(ctx context.Context, rec app.TranscriptRecord) error {
	if rec.SHA256 == ([32]byte{}) {
		return errors.New("sqlitehistory: TranscriptStore.Upsert: zero sha256")
	}
	createdAt := rec.CreatedAt
	if createdAt == 0 {
		createdAt = s.clock().Unix()
	}
	const q = `INSERT OR REPLACE INTO media_transcripts
	             (sha256, transcript, lang, adapter, created_at)
	             VALUES (?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, rec.SHA256[:], rec.Transcript, rec.Lang, rec.Adapter, createdAt); err != nil {
		return fmt.Errorf("sqlitehistory: TranscriptStore.Upsert: %w", err)
	}
	return nil
}

// Get implements app.TranscriptStore. Returns the zero record + nil
// error when no row exists for sha. Callers MUST check Transcript=="" +
// nil err to distinguish "not transcribed yet" from a real db failure.
func (s *TranscriptStore) Get(ctx context.Context, sha [32]byte) (app.TranscriptRecord, error) {
	if sha == ([32]byte{}) {
		return app.TranscriptRecord{}, errors.New("sqlitehistory: TranscriptStore.Get: zero sha256")
	}
	const q = `SELECT transcript, lang, adapter, created_at
	             FROM media_transcripts WHERE sha256 = ?`
	var out app.TranscriptRecord
	out.SHA256 = sha
	err := s.db.QueryRowContext(ctx, q, sha[:]).Scan(&out.Transcript, &out.Lang, &out.Adapter, &out.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return app.TranscriptRecord{}, nil
	case err != nil:
		return app.TranscriptRecord{}, fmt.Errorf("sqlitehistory: TranscriptStore.Get: %w", err)
	}
	return out, nil
}
