package sqlitetokens

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type legacyDraftTokenRow struct {
	ID                string
	Hash              []byte
	Name, Scope       string
	Created, Expires  int64
	LastUsed, Revoked sql.NullInt64
}

func legacyDraftRows(t *testing.T, db *sql.DB) []legacyDraftTokenRow {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT token_id,hash,name,scope,created_at,expires_at,last_used_at,revoked_at FROM tokens WHERE token_id IN ('old-read','old-send','old-admin') ORDER BY token_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []legacyDraftTokenRow
	for rows.Next() {
		var a legacyDraftTokenRow
		if err := rows.Scan(&a.ID, &a.Hash, &a.Name, &a.Scope, &a.Created, &a.Expires, &a.LastUsed, &a.Revoked); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDraftScopeUpgradePreservesAllLegacyRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old-tokens.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	oldDDL := `CREATE TABLE tokens(
 token_id TEXT PRIMARY KEY, hash BLOB NOT NULL UNIQUE, name TEXT NOT NULL,
 scope TEXT NOT NULL CHECK (scope IN ('read','send','admin')),
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 last_used_at INTEGER, revoked_at INTEGER
 );
 CREATE INDEX idx_tokens_revoked ON tokens(revoked_at);
 CREATE INDEX idx_tokens_created ON tokens(created_at DESC);`
	if _, err := db.ExecContext(t.Context(), oldDDL); err != nil {
		t.Fatal(err)
	}
	raw := []string{"synthetic-read", "synthetic-send", "synthetic-admin"}
	ids := []string{"old-read", "old-send", "old-admin"}
	scopes := []string{"read", "send", "admin"}
	for i := range ids {
		sum := sha256.Sum256([]byte(raw[i]))
		var last, revoked any
		if i == 0 {
			last = int64(1234567890)
		}
		if i == 1 {
			revoked = int64(1234567800)
		}
		expires := int64(0)
		if i == 2 {
			expires = 100
		} // old expired token
		if _, err := db.ExecContext(t.Context(), `INSERT INTO tokens(token_id,hash,name,scope,created_at,expires_at,last_used_at,revoked_at) VALUES(?,?,?,?,?,?,?,?)`,
			ids[i], sum[:], "synthetic "+ids[i], scopes[i], int64(1234500000+i), expires, last, revoked); err != nil {
			t.Fatal(err)
		}
	}
	before := legacyDraftRows(t, db)
	if len(before) != 3 {
		t.Fatalf("fixture rows=%d", len(before))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migration: %v", err)
	}
	after := legacyDraftRows(t, store.db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed token rows, hashed credentials or metadata")
	}
	for i, s := range []Scope{ScopeRead, ScopeSend, ScopeAdmin} {
		tok, err := store.Verify(ctx, raw[i])
		switch i {
		case 0:
			if err != nil || tok.Scope != s {
				t.Fatalf("read token: %v %q", err, tok.Scope)
			}
		case 1:
			if !errors.Is(err, ErrTokenRevoked) || tok.Scope != s {
				t.Fatalf("revoked token: %v %q", err, tok.Scope)
			}
		case 2:
			if !errors.Is(err, ErrTokenExpired) || tok.Scope != s {
				t.Fatalf("expired token: %v %q", err, tok.Scope)
			}
		}
	}
	tok, err := store.Issue(ctx, "synthetic draft", ScopeDraft, 24*time.Hour)
	if err != nil {
		t.Fatalf("draft issue: %v", err)
	}
	if tok.Scope != ScopeDraft || len(tok.Raw) == 0 {
		t.Fatal("draft not issued")
	}
	got, err := store.Verify(ctx, tok.Raw)
	if err != nil || got.Scope != ScopeDraft || got.Raw != "" {
		t.Fatalf("draft verify: %v %q", err, got.Scope)
	}
	if _, err := store.Issue(ctx, "bad", Scope("root"), 0); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("invalid scope: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// A successful Verify intentionally updates last_used_at on Close.
	// Capture the post-use database; a subsequent Open must not change it.
	check, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	postUse := legacyDraftRows(t, check)
	if err := check.Close(); err != nil {
		t.Fatal(err)
	}
	re, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = re.Close() }()
	if got := legacyDraftRows(t, re.db); !reflect.DeepEqual(postUse, got) {
		t.Fatal("idempotent reopen changed token rows")
	}
	if _, err := re.Verify(ctx, tok.Raw); err != nil {
		t.Fatal("draft lost after reopen:", err)
	}
	var schema string
	if err := re.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE name='tokens'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'draft'") {
		t.Fatal("new scope constraint absent")
	}
	var count int
	if err := re.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_tokens_revoked','idx_tokens_created')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("index count %d %v", count, err)
	}
}

func TestDraftScopeUpgradeUnknownSchemaRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE tokens(token_id TEXT PRIMARY KEY,scope TEXT NOT NULL CHECK (scope IN ('read','super')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tokens VALUES ('fixture','read')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); err == nil {
		t.Fatal("unrecognized schema accepted")
	}
	db, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var id, scope string
	if err := db.QueryRowContext(t.Context(), `SELECT token_id,scope FROM tokens`).Scan(&id, &scope); err != nil {
		t.Fatal(err)
	}
	if id != "fixture" || scope != "read" {
		t.Fatal("migration failure changed row")
	}
	var schema string
	if err := db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE name='tokens'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'super'") {
		t.Fatal("unknown schema was replaced")
	}
}
