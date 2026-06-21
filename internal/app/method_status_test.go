package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// handleStatus drives the "status" method and decodes the connected + jid
// fields shared by the paired/not-paired assertions.
func handleStatus(t *testing.T, d *app.Dispatcher) (connected bool, jid string) {
	t.Helper()
	result, err := d.Handle(context.Background(), "status", nil)
	if err != nil {
		t.Fatalf("Handle(status): %v", err)
	}
	var res struct {
		Connected bool   `json:"connected"`
		JID       string `json:"jid"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return res.Connected, res.JID
}

// TestStatus_NotPaired pins FR-027: with a zero session, status reports
// connected=false and omits the JID.
func TestStatus_NotPaired(t *testing.T) {
	d, _ := newTestDispatcher(t, 0)

	connected, jid := handleStatus(t, d)
	if connected {
		t.Error("connected = true, want false")
	}
	if jid != "" {
		t.Errorf("jid = %q, want empty", jid)
	}
}

// TestStatus_Paired pins FR-027: a saved session flips connected=true
// and surfaces the session JID.
func TestStatus_Paired(t *testing.T) {
	d, adapter := newTestDispatcher(t, 0)

	sess, err := domain.NewSession(domain.MustJID(testJIDStr), 1, time.Now())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := adapter.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	connected, jid := handleStatus(t, d)
	if !connected {
		t.Error("connected = false, want true")
	}
	if jid != testJIDStr {
		t.Errorf("jid = %q, want %q", jid, testJIDStr)
	}
}

// TestGroups_Empty pins FR-028: no groups yields an empty (non-null)
// groups array.
func TestGroups_Empty(t *testing.T) {
	d, _ := newTestDispatcher(t, 0)

	result, err := d.Handle(context.Background(), "groups", nil)
	if err != nil {
		t.Fatalf("Handle(groups): %v", err)
	}
	var res struct {
		Groups []json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Groups == nil {
		t.Error("groups = null, want []")
	}
	if len(res.Groups) != 0 {
		t.Errorf("groups = %d entries, want 0", len(res.Groups))
	}
}

// TestGroups_List pins FR-028: seeded groups come back with jid,
// subject, and stringified participants.
func TestGroups_List(t *testing.T) {
	d, adapter := newTestDispatcher(t, 0)

	member := domain.MustJID(testJIDStr)
	groupJID := domain.MustJID("120363021033254949@g.us")
	adapter.SeedGroup(domain.Group{
		JID:          groupJID,
		Subject:      "ops",
		Participants: []domain.JID{member},
		FetchedAt:    time.Now(),
	})

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
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.Groups) != 1 {
		t.Fatalf("groups = %d entries, want 1", len(res.Groups))
	}
	g := res.Groups[0]
	if g.JID != groupJID.String() {
		t.Errorf("jid = %q, want %q", g.JID, groupJID.String())
	}
	// FR-005a: the subject is attacker-controllable and must not leak raw;
	// it lives in the channel envelope (verified in firewall_views_test.go).
	if g.Subject != "" {
		t.Errorf("subject = %q, want empty", g.Subject)
	}
	if len(g.Participants) != 1 || g.Participants[0] != member.String() {
		t.Errorf("participants = %v, want [%s]", g.Participants, member.String())
	}
}
