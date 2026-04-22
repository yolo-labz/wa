package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// profileSetNameParams is the JSON-RPC params for "profile.setName"
// (FR-026). Length is enforced by the adapter (≤ 25 bytes per the
// server-side cap).
type profileSetNameParams struct {
	Name string `json:"name"`
}

// profileSetStatusParams is the JSON-RPC params for "profile.setStatus"
// (FR-027). Length is enforced by the adapter (≤ 139 bytes).
type profileSetStatusParams struct {
	Status string `json:"status"`
}

// profileAvatarParams is the JSON-RPC params for "profile.avatar"
// (FR-028). size is "preview" or "full"; empty defaults to preview.
type profileAvatarParams struct {
	JID  string `json:"jid"`
	Size string `json:"size,omitempty"`
}

// profileAvatarResult is the JSON-RPC result for "profile.avatar". The
// path lives on the daemon's filesystem; the client is responsible for
// reading the bytes locally (unix-socket implies same-host).
type profileAvatarResult struct {
	Path string `json:"path"`
}

// handleProfileSetName implements "profile.setName" (FR-026).
// Idempotency-wrapped because the adapter writes an AuditProfileEdit
// entry and a replay of the same (key, name) tuple must not double-audit.
func (d *Dispatcher) handleProfileSetName(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "profile.setName", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doProfileSetName(ctx, raw)
	})
}

func (d *Dispatcher) doProfileSetName(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.profileEd == nil {
		return nil, ErrMethodNotFound
	}
	var p profileSetNameParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.profileEd.SetName(ctx, p.Name); err != nil {
		return nil, fmt.Errorf("profile.setName: %w", err)
	}
	return json.Marshal(struct{}{})
}

// handleProfileSetStatus implements "profile.setStatus" (FR-027).
func (d *Dispatcher) handleProfileSetStatus(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "profile.setStatus", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doProfileSetStatus(ctx, raw)
	})
}

func (d *Dispatcher) doProfileSetStatus(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.profileEd == nil {
		return nil, ErrMethodNotFound
	}
	var p profileSetStatusParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.profileEd.SetStatus(ctx, p.Status); err != nil {
		return nil, fmt.Errorf("profile.setStatus: %w", err)
	}
	return json.Marshal(struct{}{})
}

// handleProfileAvatar implements "profile.avatar" (FR-028). Read-only —
// no idempotency sidecar. The adapter's (JID, size) cache de-duplicates
// repeated calls at the HTTP layer.
func (d *Dispatcher) handleProfileAvatar(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.profileEd == nil {
		return nil, ErrMethodNotFound
	}
	var p profileAvatarParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.JID == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, ErrInvalidJID
	}
	size, err := domain.ParseAvatarSize(p.Size)
	if err != nil {
		return nil, ErrInvalidParams
	}
	path, err := d.profileEd.GetAvatar(ctx, jid, size)
	if err != nil {
		return nil, fmt.Errorf("profile.avatar: %w", err)
	}
	return json.Marshal(profileAvatarResult{Path: path})
}
