package sqlitecontacts_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestContactSearcherContract runs the T1-18 standalone runner against
// the SQLite adapter — the authoritative ContactSearcher impl.
func TestContactSearcherContract(t *testing.T) {
	porttest.RunContactSearcherContract(t, func(t *testing.T) app.ContactSearcher {
		return openStore(t)
	})
}

func openStore(t *testing.T) *sqlitecontacts.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlitecontacts.Open(context.Background(), filepath.Join(dir, "contacts.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestContactsMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contacts.db")
	for i := range 3 {
		s, err := sqlitecontacts.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

func TestUpsertLookup(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	c, _ := domain.NewContact(jid, "Alice")
	if err := s.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.Lookup(ctx, jid)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.PushName != "Alice" {
		t.Fatalf("push_name: got %q want Alice", got.PushName)
	}
}

// A directory miss must be the typed -32119 refusal (#46), and still be
// sqlitecontacts.ErrNotFound for callers that match on the adapter error.
func TestLookupMissIsTypedContactNotFound(t *testing.T) {
	s := openStore(t)
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	_, err := s.Lookup(context.Background(), jid)
	if !errors.Is(err, app.ErrContactNotFound) || !errors.Is(err, sqlitecontacts.ErrNotFound) {
		t.Fatalf("Lookup miss = %v, want both app.ErrContactNotFound and sqlitecontacts.ErrNotFound", err)
	}
}

func TestSearchTrigram(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for i, name := range []string{"Alice Silva", "Bob Alvarez", "Carlos"} {
		jid, _ := domain.Parse("55119999999" + string(rune('0'+i)) + "@s.whatsapp.net")
		c, _ := domain.NewContact(jid, name)
		if err := s.Upsert(ctx, c); err != nil {
			t.Fatalf("Upsert %s: %v", name, err)
		}
	}
	hits, err := s.Search(ctx, "alv", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search alv: 0 hits, want ≥1")
	}
}

func TestListChanged(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	c, _ := domain.NewContact(jid, "Alice")
	if err := s.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.ListChanged(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListChanged: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d want 1", len(got))
	}
}

func TestSyncModeValid(t *testing.T) {
	s := openStore(t)
	if err := s.Sync(context.Background(), app.SyncDelta); err != nil {
		t.Fatalf("Sync delta: %v", err)
	}
	if err := s.Sync(context.Background(), 99); err == nil {
		t.Fatalf("Sync invalid mode: want error")
	}
}

// TestSearchQuotesCallerText (WA-RESOLVE-CONTACT-FTS): names with FTS5 syntax
// characters were a SQL syntax error (-32603); they are data and match
// as plain substrings.
func TestSearchQuotesCallerText(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	names := map[string]string{
		"5511915554658@s.whatsapp.net": "Ana-Maria Souza",
		"5511915556522@s.whatsapp.net": `Pat "Boss" O'Brien`,
		"5511915557417@s.whatsapp.net": "ana@example.com",
		"5511915559180@s.whatsapp.net": "Rua: Norte",
		"5511915559082@s.whatsapp.net": "Maria Souza",
	}
	for j, name := range names {
		jid, _ := domain.Parse(j)
		c, _ := domain.NewContact(jid, name)
		if err := s.Upsert(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]string{
		"Ana-Maria": "5511915554658", `"Boss"`: "5511915556522", `O"B`: "", "ana@example": "5511915557417",
		"a: N": "5511915559180", "Maria Souza": "5511915559082", "NOT Souza": "", "(Souza": "", "Souza*": "", "-Maria": "5511915554658",
	} {
		hits, err := s.Search(ctx, q, 10)
		if err != nil {
			t.Errorf("Search(%q): %v", q, err)
			continue
		}
		found := false
		for _, h := range hits {
			found = found || strings.HasPrefix(h.JID.String(), want)
		}
		if want != "" && !found || want == "" && len(hits) != 0 {
			t.Errorf("Search(%q) = %d hits, want %q", q, len(hits), want)
		}
	}
}
