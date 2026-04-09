package app

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/internal/domain"
)

// pairParams is the JSON-RPC params for the "pair" method.
type pairParams struct {
	Phone string `json:"phone,omitempty"`
}

// pairResult is the JSON-RPC result for "pair".
type pairResult struct {
	Paired bool   `json:"paired"`
	Code   string `json:"code,omitempty"`
}

// handlePair implements the "pair" JSON-RPC method (FR-023..FR-026).
//
// It bypasses the safety pipeline entirely (FR-024): pairing is a
// bootstrap action that must work before any allowlist or rate limiter
// state exists.
//
// If a session already exists (non-zero Session from SessionStore.Load),
// handlePair returns ErrNotPaired so the caller knows they must unlink
// first (FR-025).
//
// NOTE: Actual adapter-level pairing (QR generation, phone-code exchange)
// is delegated to the composition root in feature 006. The app layer
// validates preconditions and returns a success stub. The memory fake
// satisfies this contract because Load returns a zero session by default.
func (d *Dispatcher) handlePair(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p pairParams
	// Phone is optional, so nil/empty params are valid — default to QR flow.
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}

	// Check for existing session (FR-025).
	sess, err := d.session.Load(ctx)
	if err != nil {
		return nil, err
	}
	if !sess.IsZero() {
		d.recordPairAudit(ctx, "denied:already-paired")
		return nil, ErrNotPaired
	}

	// Delegate actual pairing to the composition root (feature 006).
	// The app layer only validates preconditions.
	result := pairResult{Paired: true}
	if p.Phone != "" {
		// Phone-code flow: the real code comes from whatsmeow.Client.PairPhone.
		// Stub until the composition root wires it.
		result.Code = "12345678"
	}

	d.recordPairAudit(ctx, "ok")

	return marshalResult(result)
}

// recordPairAudit records an audit event for a pairing attempt.
func (d *Dispatcher) recordPairAudit(ctx context.Context, decision string) {
	evt := domain.NewAuditEvent("dispatcher", domain.AuditPair, domain.JID{}, decision, "")
	if err := d.audit.Record(ctx, evt); err != nil {
		d.log.Error("audit write failed", "err", err)
	}
}
