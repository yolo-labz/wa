package sqlitehistory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestV2ToV3Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		var version int
		if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatalf("user_version scan: %v", err)
		}
		if version != 3 {
			t.Fatalf("user_version: got %d want 3", version)
		}
		if !hasColumn(ctx, s.db, "messages", "interactive_json") {
			t.Fatalf("messages.interactive_json missing after migrate")
		}
		var name string
		err = s.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name='message_receipts'",
		).Scan(&name)
		if err != nil {
			t.Fatalf("message_receipts table missing: %v", err)
		}
		err = s.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name='message_idempotency'",
		).Scan(&name)
		if err != nil {
			t.Fatalf("message_idempotency table missing: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}
