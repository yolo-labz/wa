package sqlitehistory

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenSetsWALBoundingPragmas asserts that Open() configures
// journal_size_limit and wal_autocheckpoint on every connection so
// the messages.db -wal file cannot grow without bound under steady
// write load (PR #137 / 09/05/2026 memory-leak investigation §A3).
//
// Without these pragmas the default `wal_autocheckpoint=1000 pages`
// (~4 MiB) plus any long-held read txn (FTS5 query, subscribe long-
// poll) can grow the WAL to multi-GB. See loke.dev "20 GB WAL"
// postmortem and SQLite docs §3.4 of wal.html.
func TestOpenSetsWALBoundingPragmas(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const (
		wantJournalSizeLimit  = int64(67108864) // 64 MiB
		wantWalAutocheckpoint = int64(256)      // ~1 MiB
	)

	var jsl int64
	if err := s.db.QueryRowContext(context.Background(),
		"PRAGMA journal_size_limit").Scan(&jsl); err != nil {
		t.Fatalf("PRAGMA journal_size_limit: %v", err)
	}
	if jsl != wantJournalSizeLimit {
		t.Errorf("journal_size_limit = %d, want %d", jsl, wantJournalSizeLimit)
	}

	var wac int64
	if err := s.db.QueryRowContext(context.Background(),
		"PRAGMA wal_autocheckpoint").Scan(&wac); err != nil {
		t.Fatalf("PRAGMA wal_autocheckpoint: %v", err)
	}
	if wac != wantWalAutocheckpoint {
		t.Errorf("wal_autocheckpoint = %d, want %d", wac, wantWalAutocheckpoint)
	}
}
