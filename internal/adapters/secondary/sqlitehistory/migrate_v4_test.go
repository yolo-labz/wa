package sqlitehistory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openV4TestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := migrateIfNeeded(ctx, db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestV3ToV4Idempotent(t *testing.T) {
	t.Parallel()
	db := openV4TestDB(t)
	ctx := context.Background()
	// running migrateV4 again on a v4 db must not error
	if err := migrateV4(ctx, db); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if v != 4 {
		t.Fatalf("user_version = %d want 4", v)
	}
}

func TestV4AddsRevokeEditColumns(t *testing.T) {
	t.Parallel()
	db := openV4TestDB(t)
	ctx := context.Background()
	for _, col := range []string{"revoked_at", "edited_at", "previous_body", "edit_of"} {
		if !hasColumn(ctx, db, "messages", col) {
			t.Errorf("missing column %q after v4", col)
		}
	}
}

func TestIdempotencyTablePK(t *testing.T) {
	t.Parallel()
	db := openV4TestDB(t)
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(idempotency_keys)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	pkCols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if pk > 0 {
			pkCols[name] = true
		}
	}
	want := []string{"method", "profile", "key"}
	for _, col := range want {
		if !pkCols[col] {
			t.Errorf("idempotency_keys PK missing column %q (got %v)", col, pkCols)
		}
	}
}

func TestV4RoundTripUpDown(t *testing.T) {
	t.Parallel()
	db := openV4TestDB(t)
	ctx := context.Background()
	// insert an edit + a revoke + a surviving original row
	insert := `INSERT INTO messages
	    (chat_jid, sender_jid, message_id, ts, body, media_type, caption, is_from_me, push_name, revoked_at, edited_at, previous_body, edit_of)
	    VALUES (?, ?, ?, ?, ?, '', '', 0, '', ?, ?, ?, ?)`
	if _, err := db.ExecContext(ctx, insert, "c@g.us", "s@s.whatsapp.net", "MSG_KEEP", 1, "keep", nil, nil, nil, nil); err != nil {
		t.Fatalf("insert keep: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert, "c@g.us", "s@s.whatsapp.net", "MSG_REVOKED", 2, "gone", 100, nil, nil, nil); err != nil {
		t.Fatalf("insert revoked: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert, "c@g.us", "s@s.whatsapp.net", "MSG_EDIT", 3, "new body", nil, 200, "old body", "MSG_KEEP"); err != nil {
		t.Fatalf("insert edit: %v", err)
	}

	if err := DownV4(ctx, db); err != nil {
		t.Fatalf("DownV4: %v", err)
	}
	// Verify columns gone
	if hasColumn(ctx, db, "messages", "revoked_at") {
		t.Error("revoked_at must be dropped")
	}
	// Verify MSG_KEEP preserved, revoked + edit rows dropped
	rows, err := db.QueryContext(ctx, `SELECT message_id FROM messages ORDER BY message_id`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	if len(got) != 1 || got[0] != "MSG_KEEP" {
		t.Errorf("after down, got %v, want [MSG_KEEP]", got)
	}
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if v != 3 {
		t.Fatalf("after down, user_version = %d want 3", v)
	}
}

func TestRecordMigration(t *testing.T) {
	t.Parallel()
	db := openV4TestDB(t)
	ctx := context.Background()
	if err := RecordMigration(ctx, db, 4, "up", "/backups/m.db.1700.bak", 1700000000); err != nil {
		t.Fatalf("record: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM migration_history WHERE backup_path = '/backups/m.db.1700.bak'`).Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Errorf("migration_history row count = %d want 1", count)
	}
}
