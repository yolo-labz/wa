package whatsmeow

import (
	"context"
	"errors"
	"testing"
)

// TestIsOnWhatsAppReturnsCanonicalJID pins the adapter half of issue #354.
// whatsmeow hands us IsOnWhatsAppResponse.JID — "the canonical user ID" —
// and the adapter used to read only IsIn and drop it, which is why a send
// to a number in its other nine-digit form passed the deliverability gate
// and then failed opaquely.
func TestIsOnWhatsAppReturnsCanonicalJID(t *testing.T) {
	t.Parallel()
	const (
		queried   = "5581987200047"
		canonical = "558187200047@s.whatsapp.net"
	)
	fc := newFakeClient()
	fc.OnWhatsAppCanonical = map[string]string{queried: canonical}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	got, on, err := a.IsOnWhatsApp(context.Background(), queried)
	if err != nil {
		t.Fatalf("IsOnWhatsApp: %v", err)
	}
	if !on {
		t.Error("registered = false, want true")
	}
	if got.String() != canonical {
		t.Errorf("canonical = %q, want %q", got.String(), canonical)
	}
}

// TestIsOnWhatsAppWithoutCanonical keeps the pre-#354 contract for a
// server response that carries no canonical JID: registered, zero JID, no
// error. The gate reads a zero JID as "nothing to compare", so this is
// what makes the change back-compatible.
func TestIsOnWhatsAppWithoutCanonical(t *testing.T) {
	t.Parallel()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	got, on, err := a.IsOnWhatsApp(context.Background(), "5581999999999")
	if err != nil {
		t.Fatalf("IsOnWhatsApp: %v", err)
	}
	if !on {
		t.Error("registered = false, want true")
	}
	if !got.IsZero() {
		t.Errorf("canonical = %q, want the zero JID", got.String())
	}
}

// TestIsOnWhatsAppUnregisteredAndError covers the two negative paths the
// gate depends on: a definitive "no account" (which it turns into a hard
// refusal) and a transient failure (which it treats fail-open).
func TestIsOnWhatsAppUnregisteredAndError(t *testing.T) {
	t.Parallel()

	t.Run("unregistered", func(t *testing.T) {
		t.Parallel()
		const phone = "5581900000000"
		fc := newFakeClient()
		fc.OnWhatsAppMap = map[string]bool{phone: false}
		a := newTestAdapter(t, fc)
		t.Cleanup(func() { _ = a.Close() })

		got, on, err := a.IsOnWhatsApp(context.Background(), phone)
		if err != nil {
			t.Fatalf("IsOnWhatsApp: %v", err)
		}
		if on {
			t.Error("registered = true, want false")
		}
		if !got.IsZero() {
			t.Errorf("canonical = %q, want the zero JID", got.String())
		}
	})

	t.Run("query error is surfaced", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("usync timeout")
		fc := newFakeClient()
		fc.OnWhatsAppErr = sentinel
		a := newTestAdapter(t, fc)
		t.Cleanup(func() { _ = a.Close() })

		if _, _, err := a.IsOnWhatsApp(context.Background(), "5581999999999"); !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the query error surfaced", err)
		}
	})
}
