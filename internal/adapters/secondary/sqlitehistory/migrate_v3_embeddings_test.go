package sqlitehistory

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestLazyEmbeddingsSchemaCreate verifies that EnsureEmbeddingsSchema
// creates the message_embeddings + pending_embeddings tables on first
// call, is idempotent on second call, and bumps user_version to 4.
// Matches tasks-tier3.md row T3-10 test name.
func TestLazyEmbeddingsSchemaCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// First call — creates tables.
	if err := EnsureEmbeddingsSchema(ctx, db); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// user_version must be 4.
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if v != 4 {
		t.Fatalf("user_version: got %d want 4", v)
	}

	// Sanity: both tables exist and accept inserts.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO message_embeddings (message_id, model, dim, vec, updated_at)
		 VALUES ('m1', 'stub', 4, X'00000000', 1700000000)`); err != nil {
		t.Fatalf("message_embeddings insert: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO pending_embeddings (message_id, profile, body, enqueued_at)
		 VALUES ('m2', 'default', 'hello', 1700000000)`); err != nil {
		t.Fatalf("pending_embeddings insert: %v", err)
	}

	// Second call — idempotent no-op.
	if err := EnsureEmbeddingsSchema(ctx, db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}
