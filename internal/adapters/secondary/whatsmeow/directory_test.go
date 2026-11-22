package whatsmeow

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeContactStore is a store.ContactStore over a fixed map. Only the two
// read methods the directory uses are live.
type fakeContactStore struct {
	store.ContactStore
	all map[waTypes.JID]waTypes.ContactInfo
	err error
}

func (f fakeContactStore) GetContact(_ context.Context, u waTypes.JID) (waTypes.ContactInfo, error) {
	return f.all[u], f.err
}

func (f fakeContactStore) GetAllContacts(context.Context) (map[waTypes.JID]waTypes.ContactInfo, error) {
	return f.all, f.err
}

func directoryAdapter(t *testing.T, cs store.ContactStore) *Adapter {
	t.Helper()
	fc := newFakeClient()
	if cs != nil {
		fc.Device = &store.Device{Contacts: cs}
	}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func wid(t *testing.T, s string) waTypes.JID {
	t.Helper()
	return mustWAJID(t, s+"@s.whatsapp.net")
}

func TestLookup(t *testing.T) {
	t.Parallel()
	const num = "5511999990001"
	jid := domain.MustJID(num)
	ctx := context.Background()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	a := directoryAdapter(t, nil)
	if _, err := a.Lookup(cancelled, jid); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v, want context.Canceled", err)
	}
	if _, err := a.Lookup(ctx, domain.JID{}); !errors.Is(err, domain.ErrInvalidJID) {
		t.Errorf("zero JID: %v, want ErrInvalidJID", err)
	}
	if _, err := a.Lookup(ctx, jid); !errors.Is(err, ErrNotFound) {
		t.Errorf("no device: %v, want ErrNotFound", err)
	}
	a.SeedContact(domain.Contact{JID: jid, PushName: "Seeded"})
	if c, err := a.Lookup(ctx, jid); err != nil || c.PushName != "Seeded" {
		t.Errorf("overlay hit = %+v, %v; want the seeded contact", c, err)
	}

	tests := []struct {
		name     string
		info     waTypes.ContactInfo
		storeErr error
		wantName string
		wantErr  error
		wantAny  bool // any error except ErrNotFound
	}{
		{"push name wins", waTypes.ContactInfo{Found: true, PushName: "Push", FullName: "Full"}, nil, "Push", nil, false},
		{"full name when push is empty", waTypes.ContactInfo{Found: true, FullName: "Full"}, nil, "Full", nil, false},
		{"not found", waTypes.ContactInfo{PushName: "ignored"}, nil, "", ErrNotFound, false},
		{"store failure", waTypes.ContactInfo{}, errors.New("db locked"), "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := directoryAdapter(t, fakeContactStore{all: map[waTypes.JID]waTypes.ContactInfo{wid(t, num): tt.info}, err: tt.storeErr})
			c, err := a.Lookup(ctx, jid)
			switch {
			case tt.wantAny:
				if err == nil || errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want the store failure, not ErrNotFound", err)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case err != nil || c.PushName != tt.wantName || c.JID != jid:
				t.Fatalf("Lookup = %+v, %v; want %q for %s", c, err, tt.wantName, jid)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	a := directoryAdapter(t, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Resolve(cancelled, "5511999990001"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	got, err := a.Resolve(context.Background(), "+55 (11) 99999-0001")
	if want := domain.MustJID("5511999990001"); err != nil || got != want {
		t.Errorf("Resolve = %v, %v; want %v", got, err, want)
	}
	if _, err := a.Resolve(context.Background(), "not a phone"); err == nil {
		t.Error("a non-phone string resolved")
	}
}

func TestAllContacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	byJID := func(cs []domain.Contact) map[domain.JID]domain.Contact {
		m := map[domain.JID]domain.Contact{}
		for _, c := range cs {
			m[c.JID] = c
		}
		return m
	}

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		c, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := directoryAdapter(t, nil).AllContacts(c); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no store yields the overlay", func(t *testing.T) {
		t.Parallel()
		a := directoryAdapter(t, nil)
		a.SeedContact(domain.Contact{JID: domain.MustJID("5511999990001"), PushName: "Seeded"})
		got, err := a.AllContacts(ctx)
		if err != nil || len(got) != 1 || got[0].PushName != "Seeded" {
			t.Fatalf("AllContacts = %+v, %v", got, err)
		}
	})
	t.Run("store failure is reported", func(t *testing.T) {
		t.Parallel()
		if _, err := directoryAdapter(t, fakeContactStore{err: errors.New("db locked")}).AllContacts(ctx); err == nil {
			t.Fatal("a failing contact store reported no error")
		}
	})
	t.Run("merge rules", func(t *testing.T) {
		t.Parallel()
		a := directoryAdapter(t, fakeContactStore{all: map[waTypes.JID]waTypes.ContactInfo{
			wid(t, "5511999990001"): {PushName: "P", FullName: "F", BusinessName: "B"},
			wid(t, "5511999990002"): {FullName: "F2", BusinessName: "B2"},
			wid(t, "5511999990003"): {BusinessName: "Shop"},
			wid(t, "5511999990004"): {},
			wid(t, "5511999990005"): {PushName: "Store"},
		}})
		a.SeedContact(domain.Contact{JID: domain.MustJID("5511999990004"), PushName: "Kept"})
		a.SeedContact(domain.Contact{JID: domain.MustJID("5511999990005"), PushName: "Overlay"})
		a.SeedContact(domain.Contact{JID: domain.MustJID("5511999990006"), PushName: "OnlyOverlay"})
		got, err := a.AllContacts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := byJID(got)
		for num, want := range map[string]struct {
			name     string
			verified bool
		}{
			"5511999990001": {"P", true},
			"5511999990002": {"F2", true},
			"5511999990003": {"Shop", true},
			"5511999990004": {"Kept", false}, // an empty store entry must not clobber a richer overlay name
			"5511999990005": {"Store", false},
			"5511999990006": {"OnlyOverlay", false},
		} {
			c, ok := m[domain.MustJID(num)]
			if !ok || c.PushName != want.name || c.Verified != want.verified {
				t.Errorf("%s = %+v (present=%v), want name %q verified %v", num, c, ok, want.name, want.verified)
			}
		}
		if len(got) != 6 {
			t.Errorf("%d contacts, want 6", len(got))
		}
	})
}

func TestBestContactName(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ push, full, biz, want string }{
		{"p", "f", "b", "p"}, {"", "f", "b", "f"}, {"", "", "b", "b"}, {"", "", "", ""},
	} {
		if got := bestContactName(tt.push, tt.full, tt.biz); got != tt.want {
			t.Errorf("bestContactName(%q,%q,%q) = %q, want %q", tt.push, tt.full, tt.biz, got, tt.want)
		}
	}
}
