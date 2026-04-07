package whatsmeow

import (
	"fmt"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

// toDomain translates a whatsmeow types.JID into a domain.JID via the
// canonical string round-trip. The contract (contracts/whatsmeow-adapter.md
// §"JID translator") requires lossless translation: for every valid j
// produced by whatsmeow, domain.Parse(j.String()) succeeds and yields a
// JID whose String() equals j.String().
//
// Errors are propagated unchanged — a whatsmeow JID whose string form is
// rejected by domain.Parse is a contract violation of the whatsmeow library
// (every JID the library emits should be well-formed), but the caller
// decides how to surface that — we do NOT panic here, only in toWhatsmeow
// where the direction is "core → adapter" and a zero or bogus JID is a
// programmer error in our own use cases.
func toDomain(j waTypes.JID) (domain.JID, error) {
	return domain.Parse(j.String())
}

// toWhatsmeow translates a domain.JID into a whatsmeow types.JID via the
// same canonical string form. Per contract §"JID translator", this function
// panics if given the zero domain.JID or a JID whose canonical string fails
// waTypes.ParseJID — both indicate a contract violation by the caller.
//
// The rationale for panic-over-error is documented in the contract: every
// domain function that produces a JID either returns a non-zero value or
// returns an error, so a zero JID reaching this function means a caller
// ignored an error. Surfacing that at the offending call site via panic is
// more actionable than silently corrupting a downstream message.
func toWhatsmeow(j domain.JID) waTypes.JID {
	if j.IsZero() {
		panic("whatsmeow adapter: toWhatsmeow called with zero JID")
	}
	parsed, err := waTypes.ParseJID(j.String())
	if err != nil {
		panic(fmt.Sprintf("whatsmeow adapter: domain.JID %q failed waTypes.ParseJID: %v", j.String(), err))
	}
	return parsed
}
