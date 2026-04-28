//go:build integration

package whatsmeow

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestLabelRoundTrip30s is the FR-121 integration gate for Tier 3 T3-21.
// It asserts that a full Create → Assign → ListAssignments → Unassign →
// DeleteLabel round-trip completes inside 30 s against a paired WhatsApp
// Business burner device. The test is gated behind WA_INTEGRATION=1; CI
// never runs it (CLAUDE.md §"v0 testing strategy").
//
// First-run wiring (maintainer):
//  1. Pair a Business-account burner once via `wa pair`.
//  2. Export WA_INTEGRATION=1 and WA_LABEL_TEST_CHAT=<target chat jid>.
//  3. go test -race -tags integration -run TestLabelRoundTrip30s ./internal/adapters/secondary/whatsmeow
//
// The skeleton below is a smoke skeleton — it constructs an adapter
// against the fake client so the file compiles under the integration
// tag. Replace the openWithClient call with Open(... session, history ...)
// once burner plumbing is in place.
func TestLabelRoundTrip30s(t *testing.T) {
	requireIntegrationEnv(t)
	t.Skip("T3-21 manual integration: fill in with real Business burner on first run")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), discardLogger(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	start := time.Now()
	lbl, err := la.CreateLabel(ctx, "default", "IntegrationTest", 5)
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	chat, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse chat: %v", err)
	}
	assignment, err := domain.NewChatLabelAssignment(lbl.ID(), chat, time.Now())
	if err != nil {
		t.Fatalf("NewChatLabelAssignment: %v", err)
	}
	if err := la.Assign(ctx, "default", assignment); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	got, err := la.ListAssignments(ctx, "default", lbl.ID())
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAssignments len = %d, want 1", len(got))
	}

	if err := la.Unassign(ctx, "default", assignment); err != nil {
		t.Fatalf("Unassign: %v", err)
	}
	if err := la.DeleteLabel(ctx, "default", lbl.ID()); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("round-trip elapsed = %v, want ≤ 30 s", elapsed)
	}
}
