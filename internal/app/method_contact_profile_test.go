package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// fakeContactSearcher is a minimal app.ContactSearcher that also satisfies
// app.ContactDirectory. It records every Upsert + Lookup so tests can
// assert the refresh-on-difference behaviour of Dispatcher.RefreshPushName.
type fakeContactSearcher struct {
	mu       sync.Mutex
	store    map[domain.JID]domain.Contact
	upserts  []domain.Contact
	lookups  int
	syncMode app.SyncMode
}

func newFakeContactSearcher() *fakeContactSearcher {
	return &fakeContactSearcher{store: map[domain.JID]domain.Contact{}}
}

func (f *fakeContactSearcher) Lookup(_ context.Context, jid domain.JID) (domain.Contact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	c, ok := f.store[jid]
	if !ok {
		return domain.Contact{}, fmt.Errorf("contact not found: %s", jid)
	}
	return c, nil
}

func (f *fakeContactSearcher) Resolve(_ context.Context, phone string) (domain.JID, error) {
	return domain.ParsePhone(phone)
}

func (f *fakeContactSearcher) Search(_ context.Context, _ string, _ int) ([]domain.Contact, error) {
	return nil, nil
}

func (f *fakeContactSearcher) Annotate(_ context.Context, _ domain.JID, _ string, _ []string) error {
	return nil
}

func (f *fakeContactSearcher) ListChanged(_ context.Context, _ int64, _ int) ([]domain.Contact, error) {
	return nil, nil
}

func (f *fakeContactSearcher) Sync(_ context.Context, mode app.SyncMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncMode = mode
	return nil
}

func (f *fakeContactSearcher) Upsert(_ context.Context, c domain.Contact) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[c.JID] = c
	f.upserts = append(f.upserts, c)
	return nil
}

func (f *fakeContactSearcher) seed(c domain.Contact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[c.JID] = c
}

func (f *fakeContactSearcher) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.upserts)
}

func newContactProfileDispatcher(t *testing.T, contacts app.ContactDirectory) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       contacts,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// TestResolveConfirmE164 verifies that a well-formed E164 phone routes
// through ContactDirectory.Resolve and the result is returned as {jid}.
func TestResolveConfirmE164(t *testing.T) {
	fs := newFakeContactSearcher()
	d, _ := newContactProfileDispatcher(t, fs)

	params, _ := json.Marshal(map[string]string{"e164": "+55 11 99999-9999"})
	raw, err := d.Handle(context.Background(), "contacts.resolve.confirm", params)
	if err != nil {
		t.Fatalf("contacts.resolve.confirm: %v", err)
	}
	var res struct {
		JID string `json:"jid"`
	}
	if uErr := json.Unmarshal(raw, &res); uErr != nil {
		t.Fatalf("unmarshal: %v", uErr)
	}
	if res.JID != testJIDStr {
		t.Fatalf("jid = %q, want %q", res.JID, testJIDStr)
	}
}

// TestResolveConfirmRejectsMalformedE164 verifies that phones that fail
// domain.ParsePhone short-circuit to -32602 without hitting the adapter.
func TestResolveConfirmRejectsMalformedE164(t *testing.T) {
	cases := []struct {
		name string
		e164 string
	}{
		{"empty", ""},
		{"too short", "123"},
		{"only dashes", "---"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeContactSearcher()
			d, _ := newContactProfileDispatcher(t, fs)
			params, _ := json.Marshal(map[string]string{"e164": tc.e164})
			_, err := d.Handle(context.Background(), "contacts.resolve.confirm", params)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Fatalf("err = %v, want ErrInvalidParams", err)
			}
		})
	}
}

// TestPushNameRefreshedOnEvent verifies that RefreshPushName upserts a
// fresh pushName when the mirrored value differs, and no-ops when it
// matches (avoiding write amplification).
func TestPushNameRefreshedOnEvent(t *testing.T) {
	fs := newFakeContactSearcher()
	d, _ := newContactProfileDispatcher(t, fs)
	jid := domain.MustJID(testJIDStr)

	// First refresh: no existing row → Upsert fires.
	d.RefreshPushName(context.Background(), jid, "Alice")
	if got, want := fs.upsertCount(), 1; got != want {
		t.Fatalf("after first refresh, upserts=%d want %d", got, want)
	}

	// Seed to match the second refresh value so it no-ops.
	fs.seed(domain.Contact{JID: jid, PushName: "Alice"})
	d.RefreshPushName(context.Background(), jid, "Alice")
	if got, want := fs.upsertCount(), 1; got != want {
		t.Errorf("unchanged pushName triggered upsert: count=%d want %d", got, want)
	}

	// Third refresh with a new value → fires again.
	d.RefreshPushName(context.Background(), jid, "Alicia")
	if got, want := fs.upsertCount(), 2; got != want {
		t.Errorf("changed pushName did not trigger upsert: count=%d want %d", got, want)
	}
}

// TestPushNameRefreshNoopsOnEmptyOrZero covers the two short-circuits
// (empty pushName, zero JID) that avoid spurious lookups.
func TestPushNameRefreshNoopsOnEmptyOrZero(t *testing.T) {
	fs := newFakeContactSearcher()
	d, _ := newContactProfileDispatcher(t, fs)

	d.RefreshPushName(context.Background(), domain.MustJID(testJIDStr), "")
	d.RefreshPushName(context.Background(), domain.JID{}, "Alice")

	if fs.upsertCount() != 0 {
		t.Errorf("expected zero upserts for empty/zero inputs, got %d", fs.upsertCount())
	}
	if fs.lookups != 0 {
		t.Errorf("expected zero lookups for empty/zero inputs, got %d", fs.lookups)
	}
}

// TestPushNameRefreshNoopsWhenContactsNotSearcher verifies graceful
// degradation: when the Contacts port does not implement ContactSearcher
// (v0 adapter without a local mirror) the hook is a no-op.
func TestPushNameRefreshNoopsWhenContactsNotSearcher(t *testing.T) {
	// memory.Adapter is ContactDirectory but not ContactSearcher — perfect.
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })

	// Should not panic or error — contract says silent no-op.
	d.RefreshPushName(context.Background(), domain.MustJID(testJIDStr), "Alice")
}
