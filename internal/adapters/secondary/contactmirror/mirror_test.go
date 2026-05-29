package contactmirror_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/contactmirror"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeDir is a stand-in ContactDirectory that records delegation and returns
// canned values, so the tests can prove Mirror.Lookup/Resolve hit the live
// directory rather than answering from the local mirror store.
type fakeDir struct {
	lookupJID  domain.JID
	lookupRet  domain.Contact
	resolveArg string
	resolveRet domain.JID
}

func (f *fakeDir) Lookup(_ context.Context, jid domain.JID) (domain.Contact, error) {
	f.lookupJID = jid
	return f.lookupRet, nil
}

func (f *fakeDir) Resolve(_ context.Context, phone string) (domain.JID, error) {
	f.resolveArg = phone
	return f.resolveRet, nil
}

func mustJID(t *testing.T, s string) domain.JID {
	t.Helper()
	jid, err := domain.Parse(s)
	if err != nil {
		t.Fatalf("Parse %q: %v", s, err)
	}
	return jid
}

func mustContact(t *testing.T, s, name string) domain.Contact {
	t.Helper()
	c, err := domain.NewContact(mustJID(t, s), name)
	if err != nil {
		t.Fatalf("NewContact %q: %v", s, err)
	}
	return c
}

// newStore opens a real sqlitecontacts.Store (the authoritative
// ContactSearcher) over a temp DB so the tests exercise the FTS index and
// triggers, not a mock.
func newStore(t *testing.T) *sqlitecontacts.Store {
	t.Helper()
	s, err := sqlitecontacts.Open(context.Background(), filepath.Join(t.TempDir(), "contacts.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSyncPopulatesMirror(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	src := contactmirror.ContactSourceFunc(func(context.Context) ([]domain.Contact, error) {
		return []domain.Contact{
			mustContact(t, "5511999999991@s.whatsapp.net", "Alice Silva"),
			mustContact(t, "5511999999992@s.whatsapp.net", "Bob Alvarez"),
		}, nil
	})
	m := contactmirror.New(&fakeDir{}, store, nil, src)

	if err := m.Sync(ctx, app.SyncFull); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	hits, err := m.Search(ctx, "alv", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search alv: 0 hits after Sync, want >=1")
	}
	// Direct lookup proves the row landed regardless of FTS matching.
	got, err := store.Lookup(ctx, mustJID(t, "5511999999991@s.whatsapp.net"))
	if err != nil {
		t.Fatalf("store.Lookup: %v", err)
	}
	if got.PushName != "Alice Silva" {
		t.Fatalf("push_name: got %q want Alice Silva", got.PushName)
	}
}

func TestLookupDelegatesToDirectory(t *testing.T) {
	ctx := context.Background()
	jid := mustJID(t, "5511999999991@s.whatsapp.net")
	dir := &fakeDir{lookupRet: mustContact(t, "5511999999991@s.whatsapp.net", "Live Name")}
	m := contactmirror.New(dir, newStore(t), nil)

	got, err := m.Lookup(ctx, jid)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if dir.lookupJID != jid {
		t.Fatalf("dir.Lookup got jid %v want %v", dir.lookupJID, jid)
	}
	if got.PushName != "Live Name" {
		t.Fatalf("push_name: got %q want Live Name (mirror store must not answer Lookup)", got.PushName)
	}
}

func TestResolveDelegatesToDirectory(t *testing.T) {
	ctx := context.Background()
	want := mustJID(t, "5511999999991@s.whatsapp.net")
	dir := &fakeDir{resolveRet: want}
	m := contactmirror.New(dir, newStore(t), nil)

	got, err := m.Resolve(ctx, "+55 11 99999-9991")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dir.resolveArg != "+55 11 99999-9991" {
		t.Fatalf("dir.Resolve got %q", dir.resolveArg)
	}
	if got != want {
		t.Fatalf("resolve: got %v want %v", got, want)
	}
}

func TestSyncAllSourcesFail(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("source boom")
	src := contactmirror.ContactSourceFunc(func(context.Context) ([]domain.Contact, error) {
		return nil, boom
	})
	m := contactmirror.New(&fakeDir{}, newStore(t), nil, src)

	err := m.Sync(ctx, app.SyncFull)
	if err == nil {
		t.Fatal("Sync: want error when every source fails, got nil")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Sync error: %v, want wrap of %v", err, boom)
	}
}

func TestSyncPartialSuccess(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	good := contactmirror.ContactSourceFunc(func(context.Context) ([]domain.Contact, error) {
		return []domain.Contact{mustContact(t, "5511999999991@s.whatsapp.net", "Alice")}, nil
	})
	bad := contactmirror.ContactSourceFunc(func(context.Context) ([]domain.Contact, error) {
		return nil, errors.New("down")
	})
	m := contactmirror.New(&fakeDir{}, store, nil, good, bad)

	if err := m.Sync(ctx, app.SyncFull); err != nil {
		t.Fatalf("Sync: partial success must not error, got %v", err)
	}
	got, err := store.Lookup(ctx, mustJID(t, "5511999999991@s.whatsapp.net"))
	if err != nil {
		t.Fatalf("store.Lookup: %v", err)
	}
	if got.PushName != "Alice" {
		t.Fatalf("push_name: got %q want Alice", got.PushName)
	}
}

func TestSyncInvalidMode(t *testing.T) {
	m := contactmirror.New(&fakeDir{}, newStore(t), nil)
	if err := m.Sync(context.Background(), app.SyncMode(0)); err == nil {
		t.Fatal("Sync: want error for invalid mode, got nil")
	}
}

func TestSyncSkipsZeroJID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	// A source can legitimately yield a zero-JID contact (e.g. an unparsable
	// history sender); Sync must skip it rather than error or upsert garbage.
	src := contactmirror.ContactSourceFunc(func(context.Context) ([]domain.Contact, error) {
		return []domain.Contact{
			{JID: domain.JID{}, PushName: "Ghost"},
			mustContact(t, "5511999999991@s.whatsapp.net", "Real"),
		}, nil
	})
	m := contactmirror.New(&fakeDir{}, store, nil, src)
	if err := m.Sync(ctx, app.SyncFull); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	hits, err := store.ListChanged(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListChanged: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d contacts, want 1 (zero-JID skipped)", len(hits))
	}
}
