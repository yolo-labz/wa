package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// draftView is the on-wire shape of a Draft row.
type draftView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target,omitempty"`
	Payload   string `json:"payload"`
	State     string `json:"state"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	DecidedAt int64  `json:"decidedAt,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func viewDraft(d domain.Draft) draftView {
	return draftView{
		ID:        d.ID,
		Kind:      d.Kind.String(),
		Target:    jidString(d.TargetJID),
		Payload:   d.PayloadJSON,
		State:     d.State.String(),
		CreatedAt: d.CreatedAt,
		ExpiresAt: d.ExpiresAt,
		DecidedAt: d.DecidedAt,
		DecidedBy: string(d.DecidedBy),
		Reason:    d.Reason,
	}
}

// draftListParams is the JSON-RPC params for "draft.list".
type draftListParams struct {
	State string `json:"state,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// handleDraftList implements "draft.list".
func (d *Dispatcher) handleDraftList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.drafts == nil {
		return nil, ErrMethodNotFound
	}
	p := draftListParams{State: "pending_review", Limit: 50}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	state, err := parseDraftState(p.State)
	if err != nil {
		return nil, err
	}
	rows, err := d.drafts.List(ctx, d.profile, state, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("draft.list: %w", err)
	}
	out := make([]draftView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewDraft(r))
	}
	return marshalResult(struct {
		Drafts []draftView `json:"drafts"`
	}{out})
}

// draftGetParams is the JSON-RPC params for "draft.get".
type draftGetParams struct {
	ID string `json:"id"`
}

// handleDraftGet implements "draft.get".
func (d *Dispatcher) handleDraftGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.drafts == nil {
		return nil, ErrMethodNotFound
	}
	var p draftGetParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	row, err := d.drafts.Get(ctx, d.profile, p.ID)
	if err != nil {
		return nil, fmt.Errorf("draft.get: %w", err)
	}
	return marshalResult(struct {
		Draft draftView `json:"draft"`
	}{viewDraft(row)})
}

// draftDecisionParams is shared by "draft.approve" / "draft.reject".
type draftDecisionParams struct {
	ID     string `json:"id"`
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// handleDraftApprove implements "draft.approve".
func (d *Dispatcher) handleDraftApprove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.drafts == nil {
		return nil, ErrMethodNotFound
	}
	var p draftDecisionParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	by, err := parseDecider(p.By, domain.DraftDeciderCLI)
	if err != nil {
		return nil, err
	}
	row, err := d.drafts.Approve(ctx, d.profile, p.ID, time.Now(), by)
	if err != nil {
		return nil, fmt.Errorf("draft.approve: %w", err)
	}
	return marshalResult(struct {
		Draft draftView `json:"draft"`
	}{viewDraft(row)})
}

// handleDraftReject implements "draft.reject".
func (d *Dispatcher) handleDraftReject(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.drafts == nil {
		return nil, ErrMethodNotFound
	}
	var p draftDecisionParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	by, err := parseDecider(p.By, domain.DraftDeciderCLI)
	if err != nil {
		return nil, err
	}
	row, err := d.drafts.Reject(ctx, d.profile, p.ID, time.Now(), by, p.Reason)
	if err != nil {
		return nil, fmt.Errorf("draft.reject: %w", err)
	}
	return marshalResult(struct {
		Draft draftView `json:"draft"`
	}{viewDraft(row)})
}

func parseDraftState(s string) (domain.DraftState, error) {
	switch s {
	case "", "pending_review":
		return domain.DraftPendingReview, nil
	case "approved":
		return domain.DraftApproved, nil
	case "rejected":
		return domain.DraftRejected, nil
	case "expired":
		return domain.DraftExpired, nil
	}
	return 0, ErrInvalidParams
}

func parseDecider(s string, fallback domain.DraftDecider) (domain.DraftDecider, error) {
	if s == "" {
		return fallback, nil
	}
	dec := domain.DraftDecider(s)
	if !dec.IsValid() || dec == domain.DraftDeciderTimeout {
		return "", ErrInvalidParams
	}
	return dec, nil
}
