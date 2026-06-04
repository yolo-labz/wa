package app

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

type fakeOnWA struct {
	on    bool
	err   error
	calls []string
}

func (f *fakeOnWA) IsOnWhatsApp(_ context.Context, phone string) (bool, error) {
	f.calls = append(f.calls, phone)
	return f.on, f.err
}

// TestEnsureOnWhatsApp pins the pre-send deliverability gate (ErrNotOnWhatsApp):
//   - phone-JID with no WhatsApp account → blocked (the frete-class fix);
//   - phone-JID with an account → allowed;
//   - @lid recipient → skipped (already reachable, never queried);
//   - check error → fail-open (a transient failure must not block a send);
//   - nil checker → skipped (pre-gate behaviour preserved).
func TestEnsureOnWhatsApp(t *testing.T) {
	t.Parallel()
	phone := domain.MustJID("5581999999999@s.whatsapp.net")
	lid := domain.MustJID("146505646268660@lid")

	cases := []struct {
		name     string
		checker  OnWhatsAppChecker
		jid      domain.JID
		want     error
		wantCall bool
	}{
		{"nil checker skips", nil, phone, nil, false},
		{"phone not on WhatsApp blocks", &fakeOnWA{on: false}, phone, ErrNotOnWhatsApp, true},
		{"phone on WhatsApp allowed", &fakeOnWA{on: true}, phone, nil, true},
		{"lid recipient skipped", &fakeOnWA{on: false}, lid, nil, false},
		{"check error fails open", &fakeOnWA{err: errors.New("rate limited")}, phone, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := &Dispatcher{onWhatsApp: c.checker}
			got := d.ensureOnWhatsApp(context.Background(), c.jid)
			if !errors.Is(got, c.want) {
				t.Fatalf("ensureOnWhatsApp = %v, want %v", got, c.want)
			}
			if f, ok := c.checker.(*fakeOnWA); ok {
				if called := len(f.calls) > 0; called != c.wantCall {
					t.Errorf("checker called = %v, want %v", called, c.wantCall)
				}
				if c.wantCall && len(f.calls) == 1 && f.calls[0] != phone.User() {
					t.Errorf("queried %q, want %q (digits only, no @server)", f.calls[0], phone.User())
				}
			}
		})
	}
}
