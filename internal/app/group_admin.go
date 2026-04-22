package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// groupCreateParams is the JSON-RPC params for "group.create" (FR-020).
// Adapter-side hard caps: subject ≤ 25 bytes, ≤ 5 groups/day, ≤ 1024
// participants (WA server cap). The adapter is the single authority on
// these; the dispatcher only parses + forwards.
type groupCreateParams struct {
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
}

// groupCreateResult echoes the freshly-minted group back to the client so
// the CLI can `wa group create` and print the new JID without a follow-up
// round trip.
type groupCreateResult struct {
	JID          string   `json:"jid"`
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
}

// groupLeaveParams is the JSON-RPC params for "group.leave" (FR-023).
type groupLeaveParams struct {
	Group string `json:"group"`
}

// handleGroupCreate implements "group.create" (FR-020). Idempotency-wrapped
// because a replay at the same key must not spend a second daily-cap slot
// or double-audit.
func (d *Dispatcher) handleGroupCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.create", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doGroupCreate(ctx, raw)
	})
}

func (d *Dispatcher) doGroupCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.groupAdmin == nil {
		return nil, ErrMethodNotFound
	}
	var p groupCreateParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if len(p.Participants) == 0 {
		return nil, ErrInvalidParams
	}
	parts := make([]domain.JID, 0, len(p.Participants))
	for _, s := range p.Participants {
		jid, err := domain.Parse(s)
		if err != nil {
			return nil, ErrInvalidJID
		}
		parts = append(parts, jid)
	}
	group, err := d.groupAdmin.Create(ctx, p.Subject, parts)
	if err != nil {
		return nil, fmt.Errorf("group.create: %w", err)
	}
	out := groupCreateResult{
		JID:          group.JID.String(),
		Subject:      group.Subject,
		Participants: make([]string, 0, len(group.Participants)),
	}
	for _, j := range group.Participants {
		out.Participants = append(out.Participants, j.String())
	}
	return json.Marshal(out)
}

// handleGroupLeave implements "group.leave" (FR-023). Idempotency-wrapped
// because the adapter writes an AuditLeaveGroup entry and a replay must
// not double-audit.
func (d *Dispatcher) handleGroupLeave(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.leave", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doGroupLeave(ctx, raw)
	})
}

func (d *Dispatcher) doGroupLeave(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.groupAdmin == nil {
		return nil, ErrMethodNotFound
	}
	var p groupLeaveParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Group == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Group)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.groupAdmin.Leave(ctx, jid); err != nil {
		return nil, fmt.Errorf("group.leave: %w", err)
	}
	return json.Marshal(struct{}{})
}

// groupRosterParams is the shared JSON-RPC params shape for the four
// roster-patch methods. "group" is the target group JID; "participants"
// is the list of user JIDs to add / remove / promote / demote.
type groupRosterParams struct {
	Group        string   `json:"group"`
	Participants []string `json:"participants"`
}

// rosterOp is the function shape every roster-patch method conforms to —
// Add, Remove, Promote, Demote all take (ctx, group, jids) and return an
// error.
type rosterOp func(ctx context.Context, group domain.JID, jids []domain.JID) error

func (d *Dispatcher) parseRosterParams(raw json.RawMessage) (domain.JID, []domain.JID, error) {
	var p groupRosterParams
	if err := parseParams(raw, &p); err != nil {
		return domain.JID{}, nil, err
	}
	if p.Group == "" || len(p.Participants) == 0 {
		return domain.JID{}, nil, ErrInvalidParams
	}
	group, err := domain.Parse(p.Group)
	if err != nil {
		return domain.JID{}, nil, ErrInvalidJID
	}
	jids := make([]domain.JID, 0, len(p.Participants))
	for _, s := range p.Participants {
		jid, err := domain.Parse(s)
		if err != nil {
			return domain.JID{}, nil, ErrInvalidJID
		}
		jids = append(jids, jid)
	}
	return group, jids, nil
}

func (d *Dispatcher) runRoster(ctx context.Context, method string, raw json.RawMessage, op rosterOp) (json.RawMessage, error) {
	if d.groupAdmin == nil {
		return nil, ErrMethodNotFound
	}
	group, jids, err := d.parseRosterParams(raw)
	if err != nil {
		return nil, err
	}
	if err := op(ctx, group, jids); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return json.Marshal(struct{}{})
}

// handleGroupAddParticipants implements "group.addParticipants" (FR-021).
// Idempotency-wrapped so a replay does not double-spend the 50 adds/day
// cap or double-audit.
func (d *Dispatcher) handleGroupAddParticipants(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.addParticipants", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.runRoster(ctx, "group.addParticipants", raw, d.groupAdmin.AddParticipants)
	})
}

// handleGroupRemoveParticipants implements "group.removeParticipants" (FR-022).
func (d *Dispatcher) handleGroupRemoveParticipants(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.removeParticipants", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.runRoster(ctx, "group.removeParticipants", raw, d.groupAdmin.RemoveParticipants)
	})
}

// handleGroupPromote implements "group.promote" (FR-024).
func (d *Dispatcher) handleGroupPromote(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.promote", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.runRoster(ctx, "group.promote", raw, d.groupAdmin.Promote)
	})
}

// handleGroupDemote implements "group.demote" (FR-024).
func (d *Dispatcher) handleGroupDemote(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "group.demote", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.runRoster(ctx, "group.demote", raw, d.groupAdmin.Demote)
	})
}
