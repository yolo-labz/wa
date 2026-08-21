package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestEnsureOnWhatsAppRecipientMoved pins issue #354: a number that IS
// registered but that the server routes under a different JID must be
// refused at the gate, naming the JID that works.
//
// The pre-#354 path let this through — IsIn was true, so the gate passed —
// and the send then failed deep in whatsmeow as an opaque -32603 that no
// caller could act on without grepping the daemon's DEBUG log for the
// usync stanza. The published forms of three Brazilian vendor numbers all
// hit this on 17/08.
func TestEnsureOnWhatsAppRecipientMoved(t *testing.T) {
	t.Parallel()

	// The real case: the published thirteen-digit form is registered, but
	// the server routes it under the twelve-digit one.
	requested := domain.MustJID("5581987200047@s.whatsapp.net")
	canonical := domain.MustJID("558187200047@s.whatsapp.net")

	fake := &fakeOnWA{on: true, canonical: canonical}
	d := &Dispatcher{onWhatsApp: fake}

	err := d.ensureOnWhatsApp(context.Background(), requested)
	if err == nil {
		t.Fatal("ensureOnWhatsApp = nil, want a refusal naming the canonical JID")
	}
	if !errors.Is(err, ErrRecipientMoved) {
		t.Errorf("errors.Is(err, ErrRecipientMoved) = false; err = %v", err)
	}
	// The whole point is that the caller can retry without shell access to
	// the daemon, so the working JID has to be in the text that reaches
	// the wire.
	if !strings.Contains(err.Error(), canonical.String()) {
		t.Errorf("error text %q does not name the canonical JID %q",
			err.Error(), canonical.String())
	}
	var coder codedError
	if !errors.As(err, &coder) || coder.RPCCode() != -32020 {
		t.Errorf("RPC code = %v, want -32020", err)
	}
}

// TestEnsureOnWhatsAppAllowsSameNumber covers the silences. None of these
// may be read as a move, because each would turn a deliverable recipient
// into a hard refusal — a strictly worse failure than the one #354 fixes.
func TestEnsureOnWhatsAppAllowsSameNumber(t *testing.T) {
	t.Parallel()

	phone := domain.MustJID("5581999999999@s.whatsapp.net")
	cases := []struct {
		name string
		fake *fakeOnWA
	}{
		{
			// The server echoing the number back is agreement, not a move.
			name: "canonical equals the requested number",
			fake: &fakeOnWA{on: true, canonical: phone},
		},
		{
			// Pre-#354 daemons and any server response without a canonical
			// JID must behave exactly as before.
			name: "server offered no canonical JID",
			fake: &fakeOnWA{on: true},
		},
		{
			// A LID is how the account is addressed, not a different
			// number; comparing it against a phone would refuse everyone.
			name: "canonical is a LID, not a phone",
			fake: &fakeOnWA{on: true, canonical: domain.MustJID("50758024224979@lid")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &Dispatcher{onWhatsApp: tc.fake}
			if err := d.ensureOnWhatsApp(context.Background(), phone); err != nil {
				t.Errorf("ensureOnWhatsApp = %v, want nil (deliverable)", err)
			}
		})
	}
}

// TestEnsureOnWhatsAppMoveIsFailOpen keeps the gate's founding property
// intact: a transient check failure never blocks a legitimate send. The
// canonical JID is only ever consulted on a definitive answer.
func TestEnsureOnWhatsAppMoveIsFailOpen(t *testing.T) {
	t.Parallel()
	fake := &fakeOnWA{
		on:        true,
		canonical: domain.MustJID("558187200047@s.whatsapp.net"),
		err:       errors.New("usync timeout"),
	}
	d := &Dispatcher{onWhatsApp: fake}
	requested := domain.MustJID("5581987200047@s.whatsapp.net")
	if err := d.ensureOnWhatsApp(context.Background(), requested); err != nil {
		t.Errorf("ensureOnWhatsApp = %v, want nil (fail-open on check error)", err)
	}
}
