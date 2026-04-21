package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// labelView is the wire shape of a Label row (FR-120).
type labelView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ColorIndex int    `json:"colorIndex"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func viewLabel(l domain.Label) labelView {
	return labelView{
		ID:         l.ID(),
		Name:       l.Name(),
		ColorIndex: l.ColorIndex(),
		CreatedAt:  l.CreatedAt().Unix(),
		UpdatedAt:  l.UpdatedAt().Unix(),
	}
}

// labelAssignmentView is the wire shape of a LabelAssignment row (FR-120).
type labelAssignmentView struct {
	LabelID    string `json:"labelID"`
	TargetKind string `json:"targetKind"`
	Chat       string `json:"chat"`
	MessageID  string `json:"messageID,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

func viewLabelAssignment(a domain.LabelAssignment) labelAssignmentView {
	return labelAssignmentView{
		LabelID:    a.LabelID(),
		TargetKind: a.TargetKind().String(),
		Chat:       a.Chat().String(),
		MessageID:  string(a.MessageID()),
		CreatedAt:  a.CreatedAt().Unix(),
	}
}

// guardLabels enforces the FR-122 precondition: the labels feature flag
// MUST be on AND the paired device MUST be a WhatsApp Business account
// (or the LabelManager port MUST be wired for dry-run testing). Returns
// ErrLabelsUnsupported (-32114) on any violation.
func (d *Dispatcher) guardLabels() error {
	if !d.features.Labels {
		return ErrLabelsUnsupported
	}
	if !d.isBusiness {
		return ErrLabelsUnsupported
	}
	if d.labels == nil {
		return ErrLabelsUnsupported
	}
	return nil
}

// handleLabelsList implements "labels.list".
func (d *Dispatcher) handleLabelsList(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if err := d.guardLabels(); err != nil {
		return nil, err
	}
	rows, err := d.labels.ListLabels(ctx, d.profile)
	if err != nil {
		return nil, fmt.Errorf("labels.list: %w", err)
	}
	out := make([]labelView, 0, len(rows))
	for _, l := range rows {
		out = append(out, viewLabel(l))
	}
	return marshalResult(struct {
		Labels []labelView `json:"labels"`
	}{out})
}

// labelsCreateParams is the JSON-RPC params for "labels.create".
type labelsCreateParams struct {
	Name       string `json:"name"`
	ColorIndex int    `json:"colorIndex"`
}

// handleLabelsCreate implements "labels.create".
func (d *Dispatcher) handleLabelsCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := d.guardLabels(); err != nil {
		return nil, err
	}
	var p labelsCreateParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, ErrInvalidParams
	}
	l, err := d.labels.CreateLabel(ctx, d.profile, p.Name, p.ColorIndex)
	if err != nil {
		return nil, fmt.Errorf("labels.create: %w", err)
	}
	return marshalResult(struct {
		Label labelView `json:"label"`
	}{viewLabel(l)})
}

// labelsDeleteParams is the JSON-RPC params for "labels.delete".
type labelsDeleteParams struct {
	ID string `json:"id"`
}

// handleLabelsDelete implements "labels.delete".
func (d *Dispatcher) handleLabelsDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := d.guardLabels(); err != nil {
		return nil, err
	}
	var p labelsDeleteParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	if err := d.labels.DeleteLabel(ctx, d.profile, p.ID); err != nil {
		return nil, fmt.Errorf("labels.delete: %w", err)
	}
	return marshalResult(struct {
		Deleted bool `json:"deleted"`
	}{true})
}

// labelsAssignParams is the JSON-RPC params for "labels.assign" and
// "labels.unassign". For chat assignments messageID is empty; for message
// assignments both chat and messageID are populated.
type labelsAssignParams struct {
	LabelID   string `json:"labelID"`
	Chat      string `json:"chat"`
	MessageID string `json:"messageID,omitempty"`
}

func (p labelsAssignParams) toAssignment(at time.Time) (domain.LabelAssignment, error) {
	if p.LabelID == "" || p.Chat == "" {
		return domain.LabelAssignment{}, ErrInvalidParams
	}
	chat, err := domain.Parse(p.Chat)
	if err != nil {
		return domain.LabelAssignment{}, ErrInvalidJID
	}
	if p.MessageID == "" {
		return domain.NewChatLabelAssignment(p.LabelID, chat, at)
	}
	return domain.NewMessageLabelAssignment(p.LabelID, chat, domain.MessageID(p.MessageID), at)
}

// handleLabelsAssign implements "labels.assign".
func (d *Dispatcher) handleLabelsAssign(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := d.guardLabels(); err != nil {
		return nil, err
	}
	var p labelsAssignParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	a, err := p.toAssignment(time.Now())
	if err != nil {
		return nil, err
	}
	if err := d.labels.Assign(ctx, d.profile, a); err != nil {
		return nil, fmt.Errorf("labels.assign: %w", err)
	}
	return marshalResult(struct {
		Assignment labelAssignmentView `json:"assignment"`
	}{viewLabelAssignment(a)})
}

// handleLabelsUnassign implements "labels.unassign".
func (d *Dispatcher) handleLabelsUnassign(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := d.guardLabels(); err != nil {
		return nil, err
	}
	var p labelsAssignParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	a, err := p.toAssignment(time.Now())
	if err != nil {
		return nil, err
	}
	if err := d.labels.Unassign(ctx, d.profile, a); err != nil {
		return nil, fmt.Errorf("labels.unassign: %w", err)
	}
	return marshalResult(struct {
		Unassigned bool `json:"unassigned"`
	}{true})
}
