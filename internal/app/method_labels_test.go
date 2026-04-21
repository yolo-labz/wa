package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// newLabelsDispatcher wires a Dispatcher with the labels port + feature
// flag on + Business account set. Callers override any of those via the
// DispatcherConfig they pass.
func newLabelsDispatcher(t *testing.T, cfg app.DispatcherConfig) *app.Dispatcher {
	t.Helper()
	if cfg.Events == nil {
		cfg.Events = blockingEvents{}
	}
	if cfg.Allowlist == nil {
		cfg.Allowlist = allowAll{}
	}
	if cfg.Audit == nil {
		cfg.Audit = nopAudit{}
	}
	if cfg.Profile == "" {
		cfg.Profile = "default"
	}
	if cfg.SessionCreated.IsZero() {
		cfg.SessionCreated = time.Now().Add(-30 * 24 * time.Hour)
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// labelViewWire mirrors app.labelView (unexported).
type labelViewWire struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ColorIndex int    `json:"colorIndex"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// labelAssignmentViewWire mirrors app.labelAssignmentView (unexported).
type labelAssignmentViewWire struct {
	LabelID    string `json:"labelID"`
	TargetKind string `json:"targetKind"`
	Chat       string `json:"chat"`
	MessageID  string `json:"messageID,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// TestLabelsUnsupportedWhenFlagOff asserts that with Business=true but the
// labels feature flag off, every labels.* method returns -32114.
func TestLabelsUnsupportedWhenFlagOff(t *testing.T) {
	t.Parallel()
	lm := memory.NewLabelManager(nil)
	d := newLabelsDispatcher(t, app.DispatcherConfig{
		Labels:            lm,
		IsBusinessAccount: true,
		Features:          app.FeatureFlags{Labels: false},
	})

	for _, method := range []string{"labels.list", "labels.create", "labels.delete", "labels.assign", "labels.unassign"} {
		_, err := d.Handle(context.Background(), method, json.RawMessage(`{}`))
		if !errors.Is(err, app.ErrLabelsUnsupported) {
			t.Errorf("%s: got err %v, want ErrLabelsUnsupported", method, err)
		}
	}
}

// TestLabelsUnsupportedWhenPersonal asserts that with the flag on but
// IsBusinessAccount=false, every labels.* method returns -32114.
func TestLabelsUnsupportedWhenPersonal(t *testing.T) {
	t.Parallel()
	lm := memory.NewLabelManager(nil)
	d := newLabelsDispatcher(t, app.DispatcherConfig{
		Labels:            lm,
		IsBusinessAccount: false,
		Features:          app.FeatureFlags{Labels: true},
	})

	_, err := d.Handle(context.Background(), "labels.list", json.RawMessage(`{}`))
	if !errors.Is(err, app.ErrLabelsUnsupported) {
		t.Fatalf("labels.list on personal: got %v, want ErrLabelsUnsupported", err)
	}
}

// TestLabelsCreateAndList covers the happy path when both guards pass.
func TestLabelsCreateAndList(t *testing.T) {
	t.Parallel()
	lm := memory.NewLabelManager(func() time.Time {
		return time.Unix(1_700_000_000, 0).UTC()
	})
	d := newLabelsDispatcher(t, app.DispatcherConfig{
		Labels:            lm,
		IsBusinessAccount: true,
		Features:          app.FeatureFlags{Labels: true},
	})

	raw, err := d.Handle(context.Background(), "labels.create",
		json.RawMessage(`{"name":"Leads","colorIndex":4}`))
	if err != nil {
		t.Fatalf("labels.create: %v", err)
	}
	var created struct {
		Label labelViewWire `json:"label"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Label.Name != "Leads" || created.Label.ColorIndex != 4 || created.Label.ID == "" {
		t.Fatalf("unexpected create view: %+v", created.Label)
	}

	raw, err = d.Handle(context.Background(), "labels.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("labels.list: %v", err)
	}
	var list struct {
		Labels []labelViewWire `json:"labels"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Labels) != 1 || list.Labels[0].ID != created.Label.ID {
		t.Fatalf("list mismatch: %+v", list)
	}
}

// TestLabelsAssignAndUnassign covers the round-trip for both chat and
// message targets plus the -32114 guard under flag+Business.
func TestLabelsAssignAndUnassign(t *testing.T) {
	t.Parallel()
	lm := memory.NewLabelManager(nil)
	d := newLabelsDispatcher(t, app.DispatcherConfig{
		Labels:            lm,
		IsBusinessAccount: true,
		Features:          app.FeatureFlags{Labels: true},
	})

	created, err := lm.CreateLabel(context.Background(), "default", "Work", 2)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	assign := json.RawMessage(`{"labelID":"` + created.ID() + `","chat":"5511999999999@s.whatsapp.net"}`)
	if _, err := d.Handle(context.Background(), "labels.assign", assign); err != nil {
		t.Fatalf("labels.assign chat: %v", err)
	}

	msgAssign := json.RawMessage(`{"labelID":"` + created.ID() + `","chat":"5511999999999@s.whatsapp.net","messageID":"msg_01"}`)
	if _, err := d.Handle(context.Background(), "labels.assign", msgAssign); err != nil {
		t.Fatalf("labels.assign message: %v", err)
	}

	got, err := lm.ListAssignments(context.Background(), "default", created.ID())
	if err != nil {
		t.Fatalf("seed list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 assignments, got %d", len(got))
	}

	if _, err := d.Handle(context.Background(), "labels.unassign", assign); err != nil {
		t.Fatalf("labels.unassign: %v", err)
	}
	got, _ = lm.ListAssignments(context.Background(), "default", created.ID())
	if len(got) != 1 || got[0].TargetKind() != domain.LabelTargetMessage {
		t.Fatalf("after unassign chat: want 1 message-kind assignment, got %+v", got)
	}
}

// TestLabelsAssignInvalidParams asserts that empty labelID or malformed
// chat JID produces ErrInvalidParams / ErrInvalidJID respectively.
func TestLabelsAssignInvalidParams(t *testing.T) {
	t.Parallel()
	lm := memory.NewLabelManager(nil)
	d := newLabelsDispatcher(t, app.DispatcherConfig{
		Labels:            lm,
		IsBusinessAccount: true,
		Features:          app.FeatureFlags{Labels: true},
	})

	_, err := d.Handle(context.Background(), "labels.assign", json.RawMessage(`{"labelID":"","chat":"5511999999999@s.whatsapp.net"}`))
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Fatalf("empty labelID: got %v want ErrInvalidParams", err)
	}

	_, err = d.Handle(context.Background(), "labels.assign", json.RawMessage(`{"labelID":"lbl","chat":"not-a-jid"}`))
	if !errors.Is(err, app.ErrInvalidJID) {
		t.Fatalf("bad chat: got %v want ErrInvalidJID", err)
	}
}
