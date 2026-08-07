package sqlitehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/008.up.sql
var schemaV8UpSQL string

//go:embed migrations/008.down.sql
var schemaV8DownSQL string

// migrateV8 fixes #315 by adding messages.caption_folded — the accent- and
// case-folded key that --caption is matched against — and backfilling it for
// every existing row that carries a caption.
//
// The backfill cannot be done in SQL: folding is NFD-decompose, drop
// combining marks, lowercase, and SQLite ships no such function. Registering
// a Go scalar would work but binds the fold to a live connection, so a
// database opened by anything else (sqlite3 CLI, a backup restore, a future
// tool) would see the column go stale. Computing it in Go on write keeps the
// stored value self-contained.
//
// Both steps are idempotent: the ADD COLUMN is skipped when the column is
// already present, and the backfill selects only rows whose caption_folded is
// still empty, so an interrupted migration resumes where it stopped.
func migrateV8(ctx context.Context, db *sql.DB) error {
	if !hasColumn(ctx, db, "messages", "caption_folded") {
		const ddl = "ALTER TABLE messages ADD COLUMN caption_folded TEXT NOT NULL DEFAULT ''"
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("sqlitehistory: v8 add caption_folded: %w", err)
		}
	}
	if err := backfillCaptionFolded(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 8"); err != nil {
		return fmt.Errorf("sqlitehistory: v8 user_version: %w", err)
	}
	return nil
}

// backfillCaptionFolded populates caption_folded for rows that have a caption
// but no folded form yet.
//
// The whole backfill is one transaction so an interrupted run leaves the
// column either fully populated for the rows it reached or untouched, never
// half-folded with user_version already advanced. It is bounded by the number
// of media rows that carry a caption, a small minority of any history (text
// messages store ”), so it does not need batching — and every row it touches
// fires messages_au, which re-indexes an unchanged `body` into FTS.
//
// Rows are read fully before writing: sharing one connection between an open
// SELECT cursor and an UPDATE on the same table is exactly the pattern SQLite
// answers with "database table is locked".
func backfillCaptionFolded(ctx context.Context, db *sql.DB) error {
	type row struct {
		rowid  int64
		folded string
	}

	rows, err := db.QueryContext(ctx,
		"SELECT rowid, caption FROM messages WHERE caption != '' AND caption_folded = ''")
	if err != nil {
		return fmt.Errorf("sqlitehistory: v8 scan captions: %w", err)
	}
	var pending []row
	for rows.Next() {
		var id int64
		var caption string
		if err := rows.Scan(&id, &caption); err != nil {
			closeRows(rows, "backfillCaptionFolded")
			return fmt.Errorf("sqlitehistory: v8 scan caption row: %w", err)
		}
		// A caption of only combining marks folds to "", which the WHERE
		// clause above would offer again on the next run. Skipping it keeps
		// the migration from rewriting the same rows forever; the row simply
		// has no searchable form, which is correct.
		if folded := foldText(caption); folded != "" {
			pending = append(pending, row{id, folded})
		}
	}
	if err := rows.Err(); err != nil {
		closeRows(rows, "backfillCaptionFolded")
		return fmt.Errorf("sqlitehistory: v8 caption rows: %w", err)
	}
	closeRows(rows, "backfillCaptionFolded")

	if len(pending) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitehistory: v8 begin backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, "UPDATE messages SET caption_folded = ? WHERE rowid = ?")
	if err != nil {
		return fmt.Errorf("sqlitehistory: v8 prepare backfill: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range pending {
		if _, err := stmt.ExecContext(ctx, r.folded, r.rowid); err != nil {
			return fmt.Errorf("sqlitehistory: v8 backfill rowid %d: %w", r.rowid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitehistory: v8 commit backfill: %w", err)
	}
	return nil
}

// DownV8 reverses migrateV8 by dropping caption_folded. No data is lost:
// the column is a derived search key and every value is recomputable from
// `caption`, which this does not touch. Rolling back reintroduces #315 —
// --caption returns to ASCII-only case folding. See 008.down.sql.
func DownV8(ctx context.Context, db *sql.DB) error {
	if hasColumn(ctx, db, "messages", "caption_folded") {
		if _, err := db.ExecContext(ctx, "ALTER TABLE messages DROP COLUMN caption_folded"); err != nil {
			return fmt.Errorf("sqlitehistory: v8 down drop caption_folded: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		return fmt.Errorf("sqlitehistory: v8 down user_version: %w", err)
	}
	return nil
}

// silence unused-warnings; the embedded SQL is retained so a future
// doctor command can emit the raw bytes (matches v4/v5/v6/v7 pattern).
var _, _ = schemaV8UpSQL, schemaV8DownSQL
