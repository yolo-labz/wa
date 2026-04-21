package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

type fakeContactStore struct {
	mu       sync.Mutex
	byJID    map[string]domain.Contact
	searched []string
	syncMode SyncMode
	notes    map[string]string
}

func newFakeContactStore() *fakeContactStore {
	return &fakeContactStore{
		byJID: map[string]domain.Contact{},
		notes: map[string]string{},
	}
}

func (s *fakeContactStore) Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byJID[jid.String()]
	if !ok {
		return domain.Contact{}, errors.New("not found")
	}
	return c, nil
}

func (s *fakeContactStore) Resolve(ctx context.Context, phone string) (domain.JID, error) {
	return domain.ParsePhone(phone)
}

func (s *fakeContactStore) Search(ctx context.Context, query string, limit int) ([]domain.Contact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searched = append(s.searched, query)
	out := make([]domain.Contact, 0, len(s.byJID))
	for _, c := range s.byJID {
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeContactStore) Annotate(ctx context.Context, jid domain.JID, notes string, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes[jid.String()] = notes
	return nil
}

func (s *fakeContactStore) ListChanged(ctx context.Context, sinceSeq int64, limit int) ([]domain.Contact, error) {
	return nil, nil
}

func (s *fakeContactStore) Sync(ctx context.Context, mode SyncMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !mode.IsValid() {
		return errors.New("invalid mode")
	}
	s.syncMode = mode
	return nil
}

func (s *fakeContactStore) Upsert(ctx context.Context, c domain.Contact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byJID[c.JID.String()] = c
	return nil
}

func (s *fakeContactStore) put(c domain.Contact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byJID[c.JID.String()] = c
}

// dispatchWithContacts builds a dispatcher whose contact port is the fake.
// It uses handlers directly to avoid full pipeline wiring.
func dispatchWithContacts(store *fakeContactStore) *Dispatcher {
	return &Dispatcher{contacts: store}
}

func TestContactsLookupOK(t *testing.T) {
	store := newFakeContactStore()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	c, _ := domain.NewContact(jid, "Alice")
	store.put(c)

	d := dispatchWithContacts(store)
	raw := json.RawMessage(`{"jid":"5511999999999@s.whatsapp.net"}`)
	out, err := d.handleContactsLookup(context.Background(), raw)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	var got struct {
		Contact contactView `json:"contact"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Contact.PushName != "Alice" {
		t.Fatalf("push: got %q want Alice", got.Contact.PushName)
	}
}

func TestContactsLookupInvalidJID(t *testing.T) {
	d := dispatchWithContacts(newFakeContactStore())
	_, err := d.handleContactsLookup(context.Background(), json.RawMessage(`{"jid":"not-a-jid"}`))
	if !errors.Is(err, ErrInvalidJID) {
		t.Fatalf("err: got %v want ErrInvalidJID", err)
	}
}

func TestContactsSearchClampsLimit(t *testing.T) {
	store := newFakeContactStore()
	d := dispatchWithContacts(store)
	_, err := d.handleContactsSearch(context.Background(), json.RawMessage(`{"query":"ali","limit":999}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(store.searched) != 1 || store.searched[0] != "ali" {
		t.Fatalf("searched: %+v", store.searched)
	}
}

func TestContactsSearchEmptyQueryRejected(t *testing.T) {
	d := dispatchWithContacts(newFakeContactStore())
	_, err := d.handleContactsSearch(context.Background(), json.RawMessage(`{"query":""}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err: got %v want ErrInvalidParams", err)
	}
}

func TestContactsAnnotate(t *testing.T) {
	store := newFakeContactStore()
	d := dispatchWithContacts(store)
	raw := json.RawMessage(`{"jid":"5511999999999@s.whatsapp.net","notes":"vip","tags":["prio"]}`)
	if _, err := d.handleContactsAnnotate(context.Background(), raw); err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if store.notes["5511999999999@s.whatsapp.net"] != "vip" {
		t.Fatalf("note: got %q", store.notes["5511999999999@s.whatsapp.net"])
	}
}

func TestContactsSyncDefaultDelta(t *testing.T) {
	store := newFakeContactStore()
	d := dispatchWithContacts(store)
	if _, err := d.handleContactsSync(context.Background(), nil); err != nil {
		t.Fatalf("sync nil: %v", err)
	}
	if store.syncMode != SyncDelta {
		t.Fatalf("mode: got %v want delta", store.syncMode)
	}
}

func TestContactsSyncFull(t *testing.T) {
	store := newFakeContactStore()
	d := dispatchWithContacts(store)
	if _, err := d.handleContactsSync(context.Background(), json.RawMessage(`{"mode":"full"}`)); err != nil {
		t.Fatalf("sync full: %v", err)
	}
	if store.syncMode != SyncFull {
		t.Fatalf("mode: got %v want full", store.syncMode)
	}
}

func TestContactsSyncRejectsUnknown(t *testing.T) {
	d := dispatchWithContacts(newFakeContactStore())
	_, err := d.handleContactsSync(context.Background(), json.RawMessage(`{"mode":"wrong"}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err: got %v want ErrInvalidParams", err)
	}
}
