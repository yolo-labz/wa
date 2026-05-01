package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// stubIdentityResolver is a minimal IdentityResolver double for the
// dispatcher unit tests. It never errs except via the seeded ErrSeed.
type stubIdentityResolver struct {
	pnToLID map[domain.JID]domain.JID
	lidToPN map[domain.JID]domain.JID
	ErrSeed error
}

func newStubResolver() *stubIdentityResolver {
	return &stubIdentityResolver{
		pnToLID: make(map[domain.JID]domain.JID),
		lidToPN: make(map[domain.JID]domain.JID),
	}
}

func (s *stubIdentityResolver) ResolveLID(_ context.Context, pn domain.JID) (domain.JID, error) {
	if s.ErrSeed != nil {
		return domain.JID{}, s.ErrSeed
	}
	if !pn.IsUser() {
		return domain.JID{}, ErrNotIdentity
	}
	return s.pnToLID[pn], nil
}

func (s *stubIdentityResolver) ResolvePN(_ context.Context, lid domain.JID) (domain.JID, error) {
	if s.ErrSeed != nil {
		return domain.JID{}, s.ErrSeed
	}
	if !lid.IsLID() {
		return domain.JID{}, ErrNotIdentity
	}
	return s.lidToPN[lid], nil
}

func (s *stubIdentityResolver) RecordMapping(_ context.Context, pn, lid domain.JID) error {
	if s.ErrSeed != nil {
		return s.ErrSeed
	}
	s.pnToLID[pn] = lid
	s.lidToPN[lid] = pn
	return nil
}

func newTestDispatcherForIdentity(_ *testing.T, resolver IdentityResolver) *Dispatcher {
	d := &Dispatcher{
		identity: resolver,
	}
	return d
}

func decodeResolveResult(t *testing.T, raw json.RawMessage) contactResolveResult {
	t.Helper()
	var out contactResolveResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestHandleContactResolve_PNToLID_Known(t *testing.T) {
	t.Parallel()
	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")
	r := newStubResolver()
	r.pnToLID[pn] = lid
	d := newTestDispatcherForIdentity(t, r)

	params, _ := json.Marshal(map[string]any{"jid": pn.String()})
	raw, err := d.handleContactResolve(context.Background(), params)
	if err != nil {
		t.Fatalf("handleContactResolve: %v", err)
	}
	out := decodeResolveResult(t, raw)
	if out.Kind != "pn" {
		t.Errorf("Kind = %q, want %q", out.Kind, "pn")
	}
	if out.Alt != lid.String() {
		t.Errorf("Alt = %q, want %q", out.Alt, lid.String())
	}
	if !out.Known {
		t.Error("Known = false, want true")
	}
}

func TestHandleContactResolve_LIDToPN_Unknown(t *testing.T) {
	t.Parallel()
	lid := domain.MustJID("66448177246461@lid")
	r := newStubResolver()
	d := newTestDispatcherForIdentity(t, r)

	params, _ := json.Marshal(map[string]any{"jid": lid.String()})
	raw, err := d.handleContactResolve(context.Background(), params)
	if err != nil {
		t.Fatalf("handleContactResolve: %v", err)
	}
	out := decodeResolveResult(t, raw)
	if out.Kind != "lid" {
		t.Errorf("Kind = %q, want %q", out.Kind, "lid")
	}
	if out.Known {
		t.Error("Known = true, want false (no mapping seeded)")
	}
	if out.Alt != "" {
		t.Errorf("Alt = %q, want empty (no mapping seeded)", out.Alt)
	}
}

func TestHandleContactResolve_GroupRefused(t *testing.T) {
	t.Parallel()
	r := newStubResolver()
	d := newTestDispatcherForIdentity(t, r)
	params, _ := json.Marshal(map[string]any{"jid": "120363042199654321@g.us"})
	_, err := d.handleContactResolve(context.Background(), params)
	if !errors.Is(err, ErrNotIdentity) {
		t.Errorf("group JID err = %v, want ErrNotIdentity", err)
	}
}

func TestHandleContactResolve_NoResolverConfigured(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{}
	params, _ := json.Marshal(map[string]any{"jid": "5511999999999@s.whatsapp.net"})
	_, err := d.handleContactResolve(context.Background(), params)
	if !errors.Is(err, ErrMethodNotFound) {
		t.Errorf("nil resolver err = %v, want ErrMethodNotFound", err)
	}
}

func TestHandleContactResolve_EmptyJID(t *testing.T) {
	t.Parallel()
	d := newTestDispatcherForIdentity(t, newStubResolver())
	params, _ := json.Marshal(map[string]any{"jid": ""})
	_, err := d.handleContactResolve(context.Background(), params)
	if !errors.Is(err, ErrInvalidParams) {
		t.Errorf("empty jid err = %v, want ErrInvalidParams", err)
	}
}

func TestHandleContactResolve_InvalidJIDString(t *testing.T) {
	t.Parallel()
	d := newTestDispatcherForIdentity(t, newStubResolver())
	params, _ := json.Marshal(map[string]any{"jid": "garbage@unknown.server"})
	_, err := d.handleContactResolve(context.Background(), params)
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("garbage JID err = %v, want ErrInvalidJID", err)
	}
}
