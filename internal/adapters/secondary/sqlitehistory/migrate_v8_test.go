package sqlitehistory

import (
	"context"
	"database/sql"
	"testing"
)

// seedV7WithCaptions builds a faithful pre-v8 database: the v1 base schema
// plus the v2 columns (which is where `caption` comes from), stamped at
// user_version=7 with rows already in it. The rows matter — the whole point of
// v8 is that captions written before the column existed still have to become
// searchable, and a migration tested only on an empty table proves nothing
// about the corpus on Pedro's disk.
func seedV7WithCaptions(t *testing.T, db *sql.DB, captions map[string]string) {
	t.Helper()
	ctx := context.Background()
	if err := migrateV2(ctx, db); err != nil {
		t.Fatalf("seed migrateV2: %v", err)
	}
	var ts int64 = 100
	for id, caption := range captions {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO messages (chat_jid, sender_jid, message_id, ts, body, media_type, caption)
			 VALUES ('c@s.whatsapp.net','s@s.whatsapp.net',?,?,'','image/jpeg',?)`,
			id, ts, caption,
		); err != nil {
			t.Fatalf("seed row %s: %v", id, err)
		}
		ts++
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		t.Fatalf("seed user_version=7: %v", err)
	}
}

func captionFolded(t *testing.T, db *sql.DB, msgID string) string {
	t.Helper()
	var got string
	err := db.QueryRowContext(context.Background(),
		"SELECT caption_folded FROM messages WHERE message_id = ?", msgID).Scan(&got)
	if err != nil {
		t.Fatalf("read caption_folded for %s: %v", msgID, err)
	}
	return got
}

// TestMigrateV8_BackfillsExistingCaptions is the #315 regression proper. The
// fix is worthless if it only applies to messages that arrive after the
// upgrade: the captions a caller actually searches for are the ones already
// sitting in the history, so the migration has to rewrite them, not just widen
// the table.
func TestMigrateV8_BackfillsExistingCaptions(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV7WithCaptions(t, db, map[string]string{
		"m-cat":   "Segue nosso catálogo",
		"m-sao":   "Foto em Sao Paulo",
		"m-empty": "",
	})

	if err := migrateV8(ctx, db); err != nil {
		t.Fatalf("migrateV8: %v", err)
	}
	if v := userVersion(t, db); v != 8 {
		t.Fatalf("user_version = %d, want 8", v)
	}
	if !hasColumn(ctx, db, "messages", "caption_folded") {
		t.Fatal("caption_folded column missing after migrateV8")
	}

	for id, want := range map[string]string{
		"m-cat": "segue nosso catalogo",
		"m-sao": "foto em sao paulo",
		// A row with no caption has nothing to fold and keeps the column
		// default. It must not become anything else — an empty needle is
		// "no filter", so a stray value here would be matched by nothing and
		// look like data loss to whoever reads the table.
		"m-empty": "",
	} {
		if got := captionFolded(t, db, id); got != want {
			t.Errorf("caption_folded[%s] = %q, want %q", id, got, want)
		}
	}

	// The source column is untouched: the fold is a search key, never a
	// rewrite of what the sender actually typed.
	var original string
	if err := db.QueryRowContext(ctx,
		"SELECT caption FROM messages WHERE message_id = 'm-cat'").Scan(&original); err != nil {
		t.Fatalf("read caption: %v", err)
	}
	if original != "Segue nosso catálogo" {
		t.Errorf("caption = %q, want the bytes the sender wrote", original)
	}
}

// TestMigrateV8_Idempotent pins that a second run is a no-op. migrateIfNeeded
// only calls migrateV8 below version 8, but a partially-applied migration (the
// process died between the ADD COLUMN and the backfill) leaves a DB that gets
// the step again, and that re-run must converge rather than double-fold or
// error on the existing column.
func TestMigrateV8_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV7WithCaptions(t, db, map[string]string{"m-cat": "Segue nosso catálogo"})

	for i := range 3 {
		if err := migrateV8(ctx, db); err != nil {
			t.Fatalf("migrateV8 #%d: %v", i, err)
		}
		if got := captionFolded(t, db, "m-cat"); got != "segue nosso catalogo" {
			t.Fatalf("caption_folded after run #%d = %q", i, got)
		}
	}
}

// TestMigrateV8_ResumesAfterInterruptedBackfill covers the crash window the
// idempotence test only implies: the column exists, some rows are folded and
// some are not. The backfill selects only rows whose caption_folded is still
// empty, precisely so a resumed run finishes the remainder rather than
// starting over.
func TestMigrateV8_ResumesAfterInterruptedBackfill(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV7WithCaptions(t, db, map[string]string{
		"m-done":    "Já FOLDADO",
		"m-pending": "Segue nosso catálogo",
	})
	// Half-applied state: column added, one row folded, the other still empty.
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE messages ADD COLUMN caption_folded TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("pre-add column: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE messages SET caption_folded = 'ja foldado' WHERE message_id = 'm-done'"); err != nil {
		t.Fatalf("pre-fold one row: %v", err)
	}

	if err := migrateV8(ctx, db); err != nil {
		t.Fatalf("migrateV8: %v", err)
	}
	if got := captionFolded(t, db, "m-pending"); got != "segue nosso catalogo" {
		t.Errorf("pending row not backfilled: caption_folded = %q", got)
	}
	if got := captionFolded(t, db, "m-done"); got != "ja foldado" {
		t.Errorf("already-folded row rewritten: caption_folded = %q", got)
	}
}

// TestDownV8_RoundTrips pins the rollback. A migration that cannot be undone
// is a one-way door on a real person's message history; the column holds only
// derived data, so dropping it must lose nothing that cannot be recomputed by
// running the migration again.
func TestDownV8_RoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV7WithCaptions(t, db, map[string]string{"m-cat": "Segue nosso catálogo"})

	if err := migrateV8(ctx, db); err != nil {
		t.Fatalf("migrateV8: %v", err)
	}
	if err := DownV8(ctx, db); err != nil {
		t.Fatalf("DownV8: %v", err)
	}
	if v := userVersion(t, db); v != 7 {
		t.Fatalf("after down, user_version = %d, want 7", v)
	}
	if hasColumn(ctx, db, "messages", "caption_folded") {
		t.Fatal("caption_folded still present after DownV8")
	}

	// The source caption survived the round trip, so re-migrating restores
	// the search key with no external input.
	if err := migrateV8(ctx, db); err != nil {
		t.Fatalf("migrateV8 after down: %v", err)
	}
	if got := captionFolded(t, db, "m-cat"); got != "segue nosso catalogo" {
		t.Errorf("caption_folded after re-migrate = %q", got)
	}
}

// TestDownV8_OnMissingColumn pins that rollback is safe to run twice, which is
// the state an operator reaches by retrying a rollback that looked like it
// failed.
func TestDownV8_OnMissingColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV7WithCaptions(t, db, nil)

	if err := DownV8(ctx, db); err != nil {
		t.Fatalf("DownV8 on a v7 DB: %v", err)
	}
	if err := DownV8(ctx, db); err != nil {
		t.Fatalf("DownV8 second run: %v", err)
	}
	if v := userVersion(t, db); v != 7 {
		t.Fatalf("user_version = %d, want 7", v)
	}
}
