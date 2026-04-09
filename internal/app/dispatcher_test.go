package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// T028: pair succeeds when no session exists.
func TestPairSucceedsNoSession(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	result, err := d.Handle(context.Background(), "pair", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handle(pair): %v", err)
	}

	var res struct {
		Paired bool   `json:"paired"`
		Code   string `json:"code,omitempty"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Paired {
		t.Error("expected paired=true")
	}
	if res.Code != "" {
		t.Errorf("expected empty code for QR flow, got %q", res.Code)
	}
}

// T028 variant: pair with phone param returns a code.
func TestPairWithPhoneReturnsCode(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	params, _ := json.Marshal(map[string]string{"phone": "+5511999999999"})
	result, err := d.Handle(context.Background(), "pair", params)
	if err != nil {
		t.Fatalf("Handle(pair): %v", err)
	}

	var res struct {
		Paired bool   `json:"paired"`
		Code   string `json:"code,omitempty"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Paired {
		t.Error("expected paired=true")
	}
	if res.Code == "" {
		t.Error("expected non-empty code for phone flow")
	}
}

// T028 variant: pair with nil params succeeds (defaults to QR flow).
func TestPairNilParams(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	result, err := d.Handle(context.Background(), "pair", nil)
	if err != nil {
		t.Fatalf("Handle(pair): %v", err)
	}

	var res struct {
		Paired bool `json:"paired"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Paired {
		t.Error("expected paired=true")
	}
}

// T029: pair returns already-paired error when session exists.
func TestPairAlreadyPaired(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)

	// Save a session so the store is non-empty.
	jid := domain.MustJID(testJIDStr)
	sess, err := domain.NewSession(jid, 1, time.Now())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := adapter.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err = d.Handle(context.Background(), "pair", json.RawMessage(`{}`))
	if !errors.Is(err, app.ErrNotPaired) {
		t.Fatalf("expected ErrNotPaired, got %v", err)
	}

	// Verify audit entry records the denial.
	entries := adapter.AuditEntries()
	found := false
	for _, e := range entries {
		if e.Decision == "denied:already-paired" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry with decision 'denied:already-paired'")
	}
}

// T039: status returns connected state when session exists.
func TestStatusReturnsConnected(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)

	// Save a session so status reports connected.
	jid := domain.MustJID(testJIDStr)
	sess, err := domain.NewSession(jid, 1, time.Now())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := adapter.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := d.Handle(context.Background(), "status", nil)
	if err != nil {
		t.Fatalf("Handle(status): %v", err)
	}

	var res struct {
		Connected bool   `json:"connected"`
		JID       string `json:"jid,omitempty"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Connected {
		t.Error("expected connected=true")
	}
	if res.JID != testJIDStr {
		t.Errorf("expected jid=%q, got %q", testJIDStr, res.JID)
	}
}

// T039 variant: status returns disconnected when no session.
func TestStatusReturnsDisconnected(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour)

	result, err := d.Handle(context.Background(), "status", nil)
	if err != nil {
		t.Fatalf("Handle(status): %v", err)
	}

	var res struct {
		Connected bool `json:"connected"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Connected {
		t.Error("expected connected=false")
	}
}

// T040: groups returns group list.
func TestGroupsReturnsList(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)

	// Seed groups.
	g1JID := domain.MustJID("120363000000000001@g.us")
	p1 := domain.MustJID("5511111111111@s.whatsapp.net")
	g1, err := domain.NewGroup(g1JID, "Group One", []domain.JID{p1})
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	adapter.SeedGroup(g1)

	g2JID := domain.MustJID("120363000000000002@g.us")
	p2 := domain.MustJID("5522222222222@s.whatsapp.net")
	g2, err := domain.NewGroup(g2JID, "Group Two", []domain.JID{p1, p2})
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	adapter.SeedGroup(g2)

	result, err := d.Handle(context.Background(), "groups", nil)
	if err != nil {
		t.Fatalf("Handle(groups): %v", err)
	}

	var res struct {
		Groups []struct {
			JID          string   `json:"jid"`
			Subject      string   `json:"subject"`
			Participants []string `json:"participants"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(res.Groups))
	}
}

// T041: status and groups bypass safety pipeline.
func TestStatusGroupsBypassSafety(t *testing.T) {
	// Create dispatcher with empty allowlist and fresh session (heavy warmup).
	// If status/groups went through safety, they would fail.
	d, _ := newTestDispatcher(t, 1*time.Hour)

	_, err := d.Handle(context.Background(), "status", nil)
	if err != nil {
		t.Fatalf("status should bypass safety: %v", err)
	}

	_, err = d.Handle(context.Background(), "groups", nil)
	if err != nil {
		t.Fatalf("groups should bypass safety: %v", err)
	}
}

// T042: markRead goes through safety pipeline.
func TestMarkReadGoesThoughSafety(t *testing.T) {
	d, _ := newTestDispatcher(t, 30*24*time.Hour) // mature session
	// Do NOT grant the JID on the allowlist.

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr, "messageId": "msg-123"})
	_, err := d.Handle(context.Background(), "markRead", params)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("expected ErrNotAllowlisted, got %v", err)
	}
}

// T042 variant: markRead succeeds when allowlisted.
func TestMarkReadSucceeds(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionRead)

	params, _ := json.Marshal(map[string]string{"chat": testJIDStr, "messageId": "msg-123"})
	result, err := d.Handle(context.Background(), "markRead", params)
	if err != nil {
		t.Fatalf("Handle(markRead): %v", err)
	}

	// markRead returns {}
	var empty struct{}
	if err := json.Unmarshal(result, &empty); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify audit entry.
	entries := adapter.AuditEntries()
	found := false
	for _, e := range entries {
		if e.Decision == "ok" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry with decision 'ok'")
	}
}

// T030: pair bypasses safety pipeline — no allowlist or rate limiter consulted.
func TestPairBypassesSafetyPipeline(t *testing.T) {
	// Create a dispatcher with a very fresh session (heavy warmup) and
	// do NOT grant any JID on the allowlist. If pair went through the
	// safety pipeline, it would fail on the allowlist check.
	d, adapter := newTestDispatcher(t, 1*time.Hour) // 1-hour-old session, heaviest warmup

	// Do NOT grant anything on the allowlist.
	// Exhaust the rate limiter by sending allowed messages first.
	// Actually, the simplest proof is: pair succeeds despite empty allowlist.
	result, err := d.Handle(context.Background(), "pair", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handle(pair): %v", err)
	}

	var res struct {
		Paired bool `json:"paired"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Paired {
		t.Error("expected paired=true")
	}

	// Verify that no rate tokens were consumed: a "send" right after
	// should still get its full burst (the rate limiter was untouched).
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "body": "hello"})
	// With a 1-hour-old session, warmup is 25% → burst=1 for per-second.
	// If pair had consumed a token, this would fail.
	_, err = d.Handle(context.Background(), "send", params)
	if err != nil {
		t.Fatalf("send after pair should succeed (no rate tokens consumed by pair): %v", err)
	}
}
