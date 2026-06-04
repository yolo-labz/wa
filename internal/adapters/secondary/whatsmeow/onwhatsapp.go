package whatsmeow

import "context"

// IsOnWhatsApp implements the app.OnWhatsAppChecker port — the primitive
// behind the pre-send deliverability gate (app.ErrNotOnWhatsApp). It asks
// the WhatsApp server whether phone (digits, no @server) is a registered
// account.
//
// Errors are surfaced, not swallowed: the app-side gate treats a check
// error fail-open (never blocks a legitimate send on a transient query
// failure), so the honest signal must reach it. A missing row for the
// queried number is reported as not-on-WhatsApp (false). A nil client
// (defensive — never the wired path) is fail-open (true).
func (a *Adapter) IsOnWhatsApp(ctx context.Context, phone string) (bool, error) {
	if a.client == nil {
		return true, nil
	}
	resp, err := a.client.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return false, err
	}
	for _, r := range resp {
		if r.IsIn {
			return true, nil
		}
	}
	return false, nil
}
