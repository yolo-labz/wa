package app

import (
	"context"
	"encoding/json"
	"fmt"
)

// handleSessionLogoutAll implements "session.logoutAll" (FR-031). Unlinks
// every device from the WhatsApp account. Routes upstream-unsupported to
// -32000 upstream_error via domain.ErrUpstreamError — mapBlockErr-style
// wrapping preserves errors.Is across the boundary.
//
// Idempotency-wrapped because a successful logout followed by a replay
// must not double-audit (the second call observes no session and returns
// nil without touching the wire).
func (d *Dispatcher) handleSessionLogoutAll(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "session.logoutAll", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSessionLogoutAll(ctx)
	})
}

func (d *Dispatcher) doSessionLogoutAll(ctx context.Context) (json.RawMessage, error) {
	if d.sessionTerm == nil {
		return nil, ErrMethodNotFound
	}
	if err := d.sessionTerm.LogoutAll(ctx); err != nil {
		return nil, fmt.Errorf("session.logoutAll: %w", err)
	}
	return json.Marshal(struct{}{})
}
