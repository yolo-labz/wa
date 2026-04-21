package sqlitehistory

import (
	"context"
	"database/sql"
	"fmt"
)

// embeddingsSchemaSQL holds the v3-additive schema that backs
// FR-101/FR-103 storage. It is only applied on first flag-on boot (see
// EnsureEmbeddingsSchema) rather than in the standard migration path
// so that personal-tier users never pay the table-creation cost.
//
// Schema rationale:
//
//   - message_embeddings carries a BLOB vec column because the pure-Go
//     brute index is the default FR-101 surface in v0.5; the sqlite-vec
//     vec0 virtual table is deferred until the WASM driver lands
//     (T3-07). The BLOB layout is 4 × dim bytes, little-endian IEEE-754.
//   - pending_embeddings is the FR-103 durable backlog. Rows are
//     inserted by the indexing pipeline on ingest and drained by the
//     worker pool; presence across restarts is the sole restart-recovery
//     contract (see embed_pipeline.go).
const embeddingsSchemaSQL = `
CREATE TABLE IF NOT EXISTS message_embeddings (
    message_id TEXT PRIMARY KEY,
    model      TEXT NOT NULL,
    dim        INTEGER NOT NULL,
    vec        BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_message_embeddings_model
    ON message_embeddings (model);

CREATE TABLE IF NOT EXISTS pending_embeddings (
    message_id  TEXT PRIMARY KEY,
    profile     TEXT NOT NULL,
    body        TEXT NOT NULL,
    enqueued_at INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_pending_embeddings_enq
    ON pending_embeddings (enqueued_at);
`

// EnsureEmbeddingsSchema creates message_embeddings + pending_embeddings
// tables and bumps PRAGMA user_version to 4. Idempotent: safe to call on
// every daemon start when the embeddings feature flag is on. Returns
// nil when the schema is already at >= 4.
func EnsureEmbeddingsSchema(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sqlitehistory: read user_version for embeddings: %w", err)
	}
	if version >= 4 {
		return nil
	}
	if _, err := db.ExecContext(ctx, embeddingsSchemaSQL); err != nil {
		return fmt.Errorf("sqlitehistory: embeddings schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		return fmt.Errorf("sqlitehistory: embeddings user_version: %w", err)
	}
	return nil
}
