package sqlitestore

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestBusinessDetectionPersists verifies IsBusiness round-trips through
// SetIsBusiness and survives a db close/reopen. Matches tasks-tier3.md
// row T3-21 test name.
func TestBusinessDetectionPersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/session.db"

	// --- session 1: mark as business --------------------------------
	db1, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	if got, err := IsBusiness(ctx, db1); err != nil || got {
		t.Fatalf("initial: got (%v, %v) want (false, nil)", got, err)
	}
	if err := SetIsBusiness(ctx, db1, true, 1_700_000_000); err != nil {
		t.Fatalf("SetIsBusiness: %v", err)
	}
	if got, err := IsBusiness(ctx, db1); err != nil || !got {
		t.Fatalf("after set: got (%v, %v) want (true, nil)", got, err)
	}
	_ = db1.Close()

	// --- session 2: flag survives reopen -----------------------------
	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if got, err := IsBusiness(ctx, db2); err != nil || !got {
		t.Fatalf("persistence: got (%v, %v) want (true, nil)", got, err)
	}

	// --- toggle back to personal ------------------------------------
	if err := SetIsBusiness(ctx, db2, false, 1_700_000_100); err != nil {
		t.Fatalf("SetIsBusiness false: %v", err)
	}
	if got, err := IsBusiness(ctx, db2); err != nil || got {
		t.Fatalf("toggle: got (%v, %v) want (false, nil)", got, err)
	}
}
