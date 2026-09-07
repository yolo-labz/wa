package app

import (
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRecipientMovedCarriesCanonicalStructurally — the opt-in follow
// recovers the canonical JID with errors.As, not by parsing English out of
// an error message. The wire contract #357 established is unchanged, so
// both properties have to hold at once.
func TestRecipientMovedCarriesCanonicalStructurally(t *testing.T) {
	t.Parallel()
	requested := domain.MustJID("5581987200047@s.whatsapp.net")
	canonical := domain.MustJID("558187200047@s.whatsapp.net")

	err := RecipientMoved(requested.String(), canonical.String())

	var moved *recipientMovedErr
	if !errors.As(err, &moved) {
		t.Fatalf("errors.As(*recipientMovedErr) = false; err = %v", err)
	}
	if moved.CanonicalJID() != canonical.String() {
		t.Errorf("CanonicalJID() = %q, want %q", moved.CanonicalJID(), canonical.String())
	}

	// Unchanged wire contract: same sentinel, same code, same text.
	if !errors.Is(err, ErrRecipientMoved) {
		t.Error("errors.Is(err, ErrRecipientMoved) = false")
	}
	var coded codedError
	if !errors.As(err, &coded) || coded.RPCCode() != -32020 {
		t.Errorf("RPCCode mismatch for %v, want -32020", err)
	}
}
