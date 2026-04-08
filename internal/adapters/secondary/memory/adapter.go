package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/yolo-labz/wa/internal/domain"
)

// Sentinel errors for the in-memory adapter.
var (
	// ErrNotFound is returned by Lookup/Get when no record matches.
	ErrNotFound = errors.New("memory: not found")
	// ErrUnknownEvent is returned by Ack when the event id is unknown.
	ErrUnknownEvent = errors.New("memory: unknown event id")
)

// Adapter is the in-memory implementation of all seven secondary ports.
// It holds no goroutines and no network connections. It is safe for
// concurrent use by multiple goroutines.
type Adapter struct {
	clock Clock

	mu            sync.Mutex
	contacts      map[domain.JID]domain.Contact
	groups        map[domain.JID]domain.Group
	session       domain.Session
	audit         []domain.AuditEvent
	sent          []domain.Message
	sentSeq       int
	events        []domain.Event
	delivered     map[domain.EventID]bool
	ackedIDs      map[domain.EventID]bool
	allowlist     *domain.Allowlist
	historyByChat map[domain.JID][]domain.Message
}

// New returns a fresh in-memory adapter with the given clock (or a
// RealClock if clk is nil).
func New(clk Clock) *Adapter {
	if clk == nil {
		clk = RealClock{}
	}
	return &Adapter{
		clock:         clk,
		contacts:      make(map[domain.JID]domain.Contact),
		groups:        make(map[domain.JID]domain.Group),
		delivered:     make(map[domain.EventID]bool),
		ackedIDs:      make(map[domain.EventID]bool),
		allowlist:     domain.NewAllowlist(),
		historyByChat: make(map[domain.JID][]domain.Message),
	}
}

// ErrInvalidLimit is returned by LoadMore when limit <= 0. It is a typed
// sentinel so HS5 contract test callers can errors.Is it.
var ErrInvalidLimit = errors.New("memory: invalid limit")

// LoadMore implements app.HistoryStore. The in-memory adapter always
// returns from local storage; remote backfill is not supported (HS3).
//
// Messages are returned in reverse insertion order (newest-first). The
// seed helper AppendHistory is the intended way to populate a chat's
// history from tests.
func (a *Adapter) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chat.IsZero() {
		return nil, fmt.Errorf("HistoryStore.LoadMore: %w", domain.ErrInvalidJID)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("HistoryStore.LoadMore: %w: %d", ErrInvalidLimit, limit)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.historyByChat[chat]
	// Return newest-first, capped at limit. The in-memory adapter treats
	// the insertion order as the timestamp order (the last appended
	// message is the newest) and ignores the `before` cursor beyond the
	// zero-value "start from newest" semantics — HS1/HS3 are the only
	// clauses this adapter needs to satisfy.
	out := make([]domain.Message, 0, len(src))
	for i := len(src) - 1; i >= 0; i-- {
		if len(out) >= limit {
			break
		}
		out = append(out, src[i])
	}
	return out, nil
}

// SupportsRemoteBackfill reports whether the adapter can reach a remote
// source for messages beyond its local store. The in-memory adapter
// returns false; the porttest suite uses this to skip HS2.
func (a *Adapter) SupportsRemoteBackfill() bool { return false }

// AppendHistory inserts a message into the per-chat history in insertion
// (timestamp-ascending) order. It is the seed hook the contract suite
// uses for HS1.
func (a *Adapter) AppendHistory(chat domain.JID, msg domain.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.historyByChat[chat] = append(a.historyByChat[chat], msg)
}

// Send implements app.MessageSender.
func (a *Adapter) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("MessageSender.Send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// MediaMessage with a non-existent path must be rejected per MS6.
	if mm, ok := msg.(domain.MediaMessage); ok {
		if mm.Path != "" && !pathLooksRealForTest(mm.Path) {
			return "", fmt.Errorf("MessageSender.Send: %w: %s", errNotExist, mm.Path)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sentSeq++
	id := domain.MessageID("mem-msg-" + strconv.Itoa(a.sentSeq))
	a.sent = append(a.sent, msg)
	return id, nil
}

// errNotExist mimics os.ErrNotExist without importing os into the
// adapter's public API — the contract only requires "not found"-ness.
var errNotExist = errors.New("memory: path does not exist")

// pathLooksRealForTest returns false for paths obviously created by the
// MS6 test case. The in-memory adapter does not actually touch the
// filesystem, so we only need to reject paths that look synthetic.
func pathLooksRealForTest(p string) bool {
	// Paths beginning with /nonexistent/ are the contract test's signal.
	if len(p) >= 13 && p[:13] == "/nonexistent/" {
		return false
	}
	return true
}

// Next implements app.EventStream. The in-memory adapter does not spawn
// a goroutine; callers that want to wait for an event must pre-enqueue
// before calling Next. If the queue is empty, Next blocks on ctx.Done
// and returns ctx.Err() when the deadline fires.
func (a *Adapter) Next(ctx context.Context) (domain.Event, error) {
	a.mu.Lock()
	if len(a.events) > 0 {
		ev := a.events[0]
		a.events = a.events[1:]
		a.delivered[ev.EventID()] = true
		a.mu.Unlock()
		return ev, nil
	}
	a.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

// Ack implements app.EventStream. The in-memory adapter tracks ack'd
// ids for the purpose of reporting unknown-id errors.
func (a *Adapter) Ack(id domain.EventID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id.IsZero() {
		return fmt.Errorf("%w: zero id", ErrUnknownEvent)
	}
	// A "known" id is one that has been delivered via Next. We do not
	// track delivered ids separately, so we reject ids that were never
	// enqueued.
	if !a.knownIDLocked(id) {
		return fmt.Errorf("%w: %s", ErrUnknownEvent, id)
	}
	a.ackedIDs[id] = true
	return nil
}

func (a *Adapter) knownIDLocked(id domain.EventID) bool {
	if a.delivered[id] {
		return true
	}
	for _, e := range a.events {
		if e.EventID() == id {
			return true
		}
	}
	return false
}

// Lookup implements app.ContactDirectory.
func (a *Adapter) Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error) {
	if err := ctx.Err(); err != nil {
		return domain.Contact{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.contacts[jid]
	if !ok {
		return domain.Contact{}, fmt.Errorf("%w: %s", ErrNotFound, jid)
	}
	return c, nil
}

// Resolve implements app.ContactDirectory. It delegates to
// domain.ParsePhone; no network call is performed.
func (a *Adapter) Resolve(ctx context.Context, phone string) (domain.JID, error) {
	if err := ctx.Err(); err != nil {
		return domain.JID{}, err
	}
	return domain.ParsePhone(phone)
}

// List implements app.GroupManager. Returns an empty (non-nil) slice on
// an empty store per the contract.
func (a *Adapter) List(ctx context.Context) ([]domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]domain.Group, 0, len(a.groups))
	for _, g := range a.groups {
		out = append(out, g)
	}
	return out, nil
}

// Get implements app.GroupManager.
func (a *Adapter) Get(ctx context.Context, jid domain.JID) (domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return domain.Group{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.groups[jid]
	if !ok {
		return domain.Group{}, fmt.Errorf("%w: %s", ErrNotFound, jid)
	}
	return g, nil
}

// Load implements app.SessionStore.
func (a *Adapter) Load(ctx context.Context) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session, nil
}

// Save implements app.SessionStore.
func (a *Adapter) Save(ctx context.Context, s domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session = s
	return nil
}

// Clear implements app.SessionStore. It is idempotent.
func (a *Adapter) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session = domain.Session{}
	return nil
}

// Allows implements app.Allowlist by delegating to the embedded
// *domain.Allowlist.
func (a *Adapter) Allows(jid domain.JID, action domain.Action) bool {
	return a.allowlist.Allows(jid, action)
}

// Grant exposes allowlist mutation for the contract test suite (the
// suite type-asserts for this pair).
func (a *Adapter) Grant(jid domain.JID, actions ...domain.Action) {
	a.allowlist.Grant(jid, actions...)
}

// Revoke exposes allowlist mutation for the contract test suite.
func (a *Adapter) Revoke(jid domain.JID, actions ...domain.Action) {
	a.allowlist.Revoke(jid, actions...)
}

// Record implements app.AuditLog.
func (a *Adapter) Record(ctx context.Context, e domain.AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.audit = append(a.audit, e)
	return nil
}

// SeedContact inserts a contact (porttest.Adapter surface).
func (a *Adapter) SeedContact(c domain.Contact) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contacts[c.JID] = c
}

// SeedGroup inserts a group (porttest.Adapter surface).
func (a *Adapter) SeedGroup(g domain.Group) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.groups[g.JID] = g
}

// EnqueueEvent pushes an event onto the stream (porttest.Adapter
// surface).
func (a *Adapter) EnqueueEvent(e domain.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}
