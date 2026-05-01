package sqlitehistory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB returns a fresh SQLite database opened on a temp file so
// the v5 migration can be exercised end-to-end alongside the v2/v3/v4
// chain.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("schema bootstrap: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatalf("seed user_version=1: %v", err)
	}
	return db
}

// TestMigrateV5_AddsColumns pins spec 107 FR-001: the v4→v5 migration
// adds sender_alt_jid and addressing_mode TEXT columns on messages,
// each nullable, and bumps user_version to 5.
func TestMigrateV5_AddsColumns(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := migrateV2(ctx, db); err != nil {
		t.Fatalf("migrateV2: %v", err)
	}
	if err := migrateV3(ctx, db); err != nil {
		t.Fatalf("migrateV3: %v", err)
	}
	if err := migrateV4(ctx, db); err != nil {
		t.Fatalf("migrateV4: %v", err)
	}
	if err := migrateV5(ctx, db); err != nil {
		t.Fatalf("migrateV5: %v", err)
	}
	for _, col := range []string{"sender_alt_jid", "addressing_mode"} {
		if !hasColumn(ctx, db, "messages", col) {
			t.Errorf("v5 migration did not add column %q", col)
		}
	}
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if v != 5 {
		t.Errorf("user_version = %d, want 5", v)
	}
}

// TestMigrateV5_Idempotent pins that re-running migrateV5 against a
// schema that already has the v5 columns is a no-op (matches the v4
// idempotency contract).
func TestMigrateV5_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := migrateV2(ctx, db); err != nil {
		t.Fatalf("migrateV2: %v", err)
	}
	if err := migrateV3(ctx, db); err != nil {
		t.Fatalf("migrateV3: %v", err)
	}
	if err := migrateV4(ctx, db); err != nil {
		t.Fatalf("migrateV4: %v", err)
	}
	if err := migrateV5(ctx, db); err != nil {
		t.Fatalf("migrateV5 #1: %v", err)
	}
	if err := migrateV5(ctx, db); err != nil {
		t.Fatalf("migrateV5 #2 (idempotent): %v", err)
	}
}

// TestStore_PersistsAndRetrievesAddressingMode pins spec 107 FR-002:
// InsertRaw stores sender_alt_jid + addressing_mode round-trip via
// QueryHistory, and missing fields surface as empty strings.
func TestStore_PersistsAndRetrievesAddressingMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	chat := "120363042199654321@g.us"

	// LID-mode inbound message with full mapping.
	if err := store.InsertRaw(ctx,
		chat, "66448177246461@lid", "MSG-LID", 1_700_000_000,
		"hi", "", "", "Ricardo", false, nil,
		"5511999999999@s.whatsapp.net", "lid",
	); err != nil {
		t.Fatalf("InsertRaw lid: %v", err)
	}

	// Legacy / history-sync row with no AddressingMode metadata.
	if err := store.InsertRaw(ctx,
		chat, "5511888888888@s.whatsapp.net", "MSG-LEGACY", 1_700_000_001,
		"old", "", "", "Alice", false, nil,
		"", "",
	); err != nil {
		t.Fatalf("InsertRaw legacy: %v", err)
	}

	msgs, err := store.QueryHistory(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}

	byID := map[string]StoredMessage{}
	for _, m := range msgs {
		byID[m.MessageID] = m
	}
	lid := byID["MSG-LID"]
	if lid.SenderAltJID != "5511999999999@s.whatsapp.net" {
		t.Errorf("LID row SenderAltJID = %q, want 5511999999999@s.whatsapp.net", lid.SenderAltJID)
	}
	if lid.AddressingMode != "lid" {
		t.Errorf("LID row AddressingMode = %q, want lid", lid.AddressingMode)
	}
	legacy := byID["MSG-LEGACY"]
	if legacy.SenderAltJID != "" {
		t.Errorf("legacy row SenderAltJID = %q, want empty", legacy.SenderAltJID)
	}
	if legacy.AddressingMode != "" {
		t.Errorf("legacy row AddressingMode = %q, want empty", legacy.AddressingMode)
	}
}
