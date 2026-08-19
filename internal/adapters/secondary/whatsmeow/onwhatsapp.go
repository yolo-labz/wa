package whatsmeow

import (
	"context"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// IsOnWhatsApp implements the app.OnWhatsAppChecker port — the primitive
// behind the pre-send deliverability gate (app.ErrNotOnWhatsApp). It asks
// the WhatsApp server whether phone (digits, no @server) is a registered
// account, and returns the canonical JID the server routes it under.
//
// The canonical JID is the server's own answer to "where does this number
// actually live" (whatsmeow: IsOnWhatsAppResponse.JID, "the canonical user
// ID"). It was previously discarded, which is why a send to a Brazilian
// number in its other nine-digit form passed this gate and then died deep
// in the send path with an opaque error. Issue #354.
//
// Errors are surfaced, not swallowed: the app-side gate treats a check
// error fail-open (never blocks a legitimate send on a transient query
// failure), so the honest signal must reach it. A missing row for the
// queried number is reported as not-on-WhatsApp (false). A nil client
// (defensive — never the wired path) is fail-open (true).
func (a *Adapter) IsOnWhatsApp(ctx context.Context, phone string) (domain.JID, bool, error) {
	if a.client == nil {
		return domain.JID{}, true, nil
	}
	resp, err := a.client.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return domain.JID{}, false, err
	}
	for _, r := range resp {
		if !r.IsIn {
			continue
		}
		// A canonical JID that will not round-trip through the domain
		// parser is no better than none: the gate only compares user
		// segments, and a malformed one must not turn a deliverable
		// number into a refusal.
		canonical, convErr := toDomain(r.JID)
		if convErr != nil {
			return domain.JID{}, true, nil
		}
		return canonical, true, nil
	}
	return domain.JID{}, false, nil
}
