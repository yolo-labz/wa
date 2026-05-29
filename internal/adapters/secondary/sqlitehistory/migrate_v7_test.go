package sqlitehistory

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
	_ "modernc.org/sqlite"
)

// seedV6BrokenTrigger turns a fresh test DB into a faithful pre-v7 v6
// database: it installs the broken messages_au trigger (the one the v1
// base schema shipped) and stamps user_version=6, so migrateV7 has
// something real to repair. A single indexed row is inserted so the
// trigger's FTS delete+reinsert has content to act on.
func seedV6BrokenTrigger(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS messages_au"); err != nil {
		t.Fatalf("drop messages_au: %v", err)
	}
	if _, err := db.ExecContext(ctx, brokenMessagesAUTrigger); err != nil {
		t.Fatalf("install broken messages_au: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		t.Fatalf("seed user_version=6: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO messages (chat_jid, sender_jid, message_id, ts, body) VALUES ('c@s.whatsapp.net','s@s.whatsapp.net','MID-1',1,'first draft')`,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// editBody runs the UPDATE that `wa msg edit` ultimately performs, firing
// the messages_au trigger. It returns the raw error so tests can assert
// the broken-vs-fixed behaviour.
func editBody(ctx context.Context, db *sql.DB, msgID, newBody string) error {
	_, err := db.ExecContext(ctx, "UPDATE messages SET body = ? WHERE message_id = ?", newBody, msgID)
	return err
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

// TestMigrateV7_RepairsBrokenTrigger is the core #171 regression: a v6 DB
// with the broken trigger fails any body UPDATE with "2 values for 3
// columns"; after migrateV7 the same UPDATE succeeds and the FTS index
// tracks the new body while dropping the old one.
func TestMigrateV7_RepairsBrokenTrigger(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV6BrokenTrigger(t, db)

	// Before: the broken trigger surfaces the exact issue-171 SQL error.
	err := editBody(ctx, db, "MID-1", "edited body")
	if err == nil {
		t.Fatal("pre-migration UPDATE unexpectedly succeeded; broken trigger not installed")
	}
	if !strings.Contains(err.Error(), "2 values for 3 columns") {
		t.Fatalf("pre-migration error = %v, want it to contain \"2 values for 3 columns\"", err)
	}

	if err := migrateV7(ctx, db); err != nil {
		t.Fatalf("migrateV7: %v", err)
	}
	if v := userVersion(t, db); v != 7 {
		t.Fatalf("user_version = %d, want 7", v)
	}

	// After: the same UPDATE succeeds.
	if err := editBody(ctx, db, "MID-1", "edited body"); err != nil {
		t.Fatalf("post-migration UPDATE: %v", err)
	}

	// FTS reflects the new body and no longer matches the old one.
	var hits int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'edited'").Scan(&hits); err != nil {
		t.Fatalf("fts match edited: %v", err)
	}
	if hits != 1 {
		t.Fatalf("fts 'edited' hits = %d, want 1", hits)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'draft'").Scan(&hits); err != nil {
		t.Fatalf("fts match draft: %v", err)
	}
	if hits != 0 {
		t.Fatalf("fts 'draft' hits = %d, want 0 (stale body still indexed)", hits)
	}

	// External-content FTS internal consistency check must pass.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO messages_fts(messages_fts) VALUES('integrity-check')"); err != nil {
		t.Fatalf("fts integrity-check: %v", err)
	}
}

// TestMigrateV7_Idempotent pins that re-running migrateV7 against an
// already-repaired schema is a harmless no-op.
func TestMigrateV7_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV6BrokenTrigger(t, db)

	if err := migrateV7(ctx, db); err != nil {
		t.Fatalf("migrateV7 #1: %v", err)
	}
	if err := migrateV7(ctx, db); err != nil {
		t.Fatalf("migrateV7 #2 (idempotent): %v", err)
	}
	if v := userVersion(t, db); v != 7 {
		t.Fatalf("user_version = %d, want 7", v)
	}
	if err := editBody(ctx, db, "MID-1", "edited again"); err != nil {
		t.Fatalf("UPDATE after double-migrate: %v", err)
	}
}

// TestDownV7_RestoresBrokenTrigger pins that the down step is a faithful
// inverse: it returns to user_version 6 and reinstates the pre-v7 (broken)
// trigger, so a body UPDATE fails again exactly as it did on v6.
func TestDownV7_RestoresBrokenTrigger(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedV6BrokenTrigger(t, db)

	if err := migrateV7(ctx, db); err != nil {
		t.Fatalf("migrateV7: %v", err)
	}
	if err := DownV7(ctx, db); err != nil {
		t.Fatalf("DownV7: %v", err)
	}
	if v := userVersion(t, db); v != 6 {
		t.Fatalf("user_version = %d, want 6", v)
	}
	if err := editBody(ctx, db, "MID-1", "edited body"); err == nil {
		t.Fatal("post-down UPDATE succeeded; faithful inverse should restore the broken trigger")
	}
}

// TestStampEditEndToEnd_Issue171 is the integration guard the bug slipped
// past: a real Store opened via Open() (which auto-migrates to v7) must let
// StampEdit overwrite a stored body without error, persist the new body,
// and keep FTS searchable on it. This exercises the corrected schema.sql
// trigger for fresh databases.
func TestStampEditEndToEnd_Issue171(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if v := userVersion(t, store.db); v < 7 {
		t.Fatalf("Open did not migrate to >=7; user_version = %d", v)
	}

	chat, err := domain.Parse("5511999990001@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Parse chat: %v", err)
	}
	const msgID = "3EB0EDIT171"
	if err := store.Insert(ctx, []StoredMessage{{
		ChatJID:   chat.String(),
		SenderJID: "me@s.whatsapp.net",
		MessageID: msgID,
		Timestamp: 1779934337,
		Body:      "first draft",
		IsFromMe:  true,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := store.StampEdit(ctx, chat, domain.MessageID(msgID), "edited body", 1779934400); err != nil {
		t.Fatalf("StampEdit (issue #171 regression): %v", err)
	}

	_, body, err := store.GetMessageMeta(ctx, chat, domain.MessageID(msgID))
	if err != nil {
		t.Fatalf("GetMessageMeta: %v", err)
	}
	if body != "edited body" {
		t.Fatalf("body after StampEdit = %q, want \"edited body\"", body)
	}

	hits, err := store.Search(ctx, "edited", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search \"edited\" returned 0 hits after StampEdit; FTS not reindexed")
	}
}
