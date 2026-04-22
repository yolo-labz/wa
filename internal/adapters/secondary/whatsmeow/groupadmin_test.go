package whatsmeow

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// newTestGroupAdmin opens a GroupAdminAdapter against a fake whatsmeow
// client wrapped by a real Adapter (so the shared auditBuf ring catches
// every write). The 4th arg (`nowFn`) lets the caller drive the per-day
// counter's midnight rollover deterministically.
func newTestGroupAdmin(t *testing.T, nowFn func() time.Time) (*GroupAdminAdapter, *fakeWhatsmeowClient, *Adapter) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), nowFn)
	t.Cleanup(func() { _ = a.Close() })
	g, err := a.NewGroupAdminFor()
	if err != nil {
		t.Fatalf("NewGroupAdminFor: %v", err)
	}
	return g, fc, a
}

// TestGroupCreateRateLimit5PerDay verifies the adapter-hard ≤ 5
// groups/day cap: 5 calls succeed, the 6th is refused with
// app.ErrRateLimitedHard, and only 5 AuditGroupCreate entries are written
// (FR-020 + adapter hard cap per ports_018.go).
func TestGroupCreateRateLimit5PerDay(t *testing.T) {
	// Freeze the clock so every create lands on the same UTC day.
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, _, a := newTestGroupAdmin(t, func() time.Time { return fixed })

	ctx := context.Background()
	participant := domain.MustJID("5511999999999")

	for i := 0; i < maxGroupCreatesPerDay; i++ {
		subject := "G" + strconv.Itoa(i)
		if _, err := g.Create(ctx, subject, []domain.JID{participant}); err != nil {
			t.Fatalf("Create #%d = %v, want nil", i+1, err)
		}
	}

	_, err := g.Create(ctx, "G6", []domain.JID{participant})
	if err == nil {
		t.Fatal("Create #6 returned nil, want ErrRateLimitedHard")
	}
	if !errors.Is(err, app.ErrRateLimitedHard) {
		t.Fatalf("Create #6 err = %v, want ErrRateLimitedHard", err)
	}

	// Verify exactly maxGroupCreatesPerDay AuditGroupCreate entries.
	var creates int
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupCreate && e.Decision == "ok" {
			creates++
		}
	}
	if creates != maxGroupCreatesPerDay {
		t.Fatalf("AuditGroupCreate count = %d, want %d", creates, maxGroupCreatesPerDay)
	}
}

// TestGroupCreateRefundOnUpstreamFailure verifies that an upstream
// CreateGroup failure does NOT permanently burn a slot: after 5 failed
// calls, a 6th successful call still succeeds.
func TestGroupCreateRefundOnUpstreamFailure(t *testing.T) {
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, fc, _ := newTestGroupAdmin(t, func() time.Time { return fixed })
	ctx := context.Background()
	participant := domain.MustJID("5511999999999")

	fc.mu.Lock()
	fc.CreateGroupErr = errors.New("transient upstream")
	fc.mu.Unlock()

	for i := 0; i < maxGroupCreatesPerDay; i++ {
		if _, err := g.Create(ctx, "G", []domain.JID{participant}); err == nil {
			t.Fatalf("Create #%d returned nil despite seeded upstream err", i+1)
		}
	}

	fc.mu.Lock()
	fc.CreateGroupErr = nil
	fc.mu.Unlock()

	if _, err := g.Create(ctx, "G-ok", []domain.JID{participant}); err != nil {
		t.Fatalf("Create after refund = %v, want nil (refund should have freed all 5 slots)", err)
	}
}

// TestGroupCreateDayRollover verifies the UTC-midnight counter reset:
// 5 creates on day N + 5 creates on day N+1 all succeed.
func TestGroupCreateDayRollover(t *testing.T) {
	now := time.Date(2026, 4, 22, 23, 0, 0, 0, time.UTC)
	g, _, _ := newTestGroupAdmin(t, func() time.Time { return now })
	ctx := context.Background()
	participant := domain.MustJID("5511999999999")

	for i := 0; i < maxGroupCreatesPerDay; i++ {
		if _, err := g.Create(ctx, "D1", []domain.JID{participant}); err != nil {
			t.Fatalf("Create day1 #%d = %v", i+1, err)
		}
	}

	now = now.Add(24 * time.Hour)

	for i := 0; i < maxGroupCreatesPerDay; i++ {
		if _, err := g.Create(ctx, "D2", []domain.JID{participant}); err != nil {
			t.Fatalf("Create day2 #%d = %v (rollover failed)", i+1, err)
		}
	}
}

// TestGroupCreateSubjectByteCap verifies the 25-byte subject cap is
// enforced before hitting the wire.
func TestGroupCreateSubjectByteCap(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	participant := domain.MustJID("5511999999999")

	subject26 := "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err := g.Create(ctx, subject26, []domain.JID{participant})
	if !errors.Is(err, domain.ErrMessageTooLarge) {
		t.Fatalf("Create(26-byte subject) err = %v, want ErrMessageTooLarge", err)
	}

	fc.mu.Lock()
	reqs := len(fc.CreateGroupReqs)
	fc.mu.Unlock()
	if reqs != 0 {
		t.Fatalf("oversized subject reached the wire: %d CreateGroupReqs", reqs)
	}
}

// TestGroupLeaveAudit verifies that a successful Leave writes exactly
// one AuditLeaveGroup entry with decision=ok for the target group.
func TestGroupLeaveAudit(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	if err := g.Leave(ctx, group); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	fc.mu.Lock()
	calls := len(fc.LeaveGroupCalls)
	fc.mu.Unlock()
	if calls != 1 {
		t.Fatalf("LeaveGroupCalls = %d, want 1", calls)
	}

	var sawLeave bool
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditLeaveGroup && e.Subject == group && e.Decision == "ok" {
			sawLeave = true
			break
		}
	}
	if !sawLeave {
		t.Fatalf("missing AuditLeaveGroup entry for %s", group)
	}
}

// TestGroupLeaveRejectsNonGroup verifies that Leave refuses a user JID.
func TestGroupLeaveRejectsNonGroup(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	userJID := domain.MustJID("5511999999999")

	err := g.Leave(ctx, userJID)
	if !errors.Is(err, domain.ErrInvalidJID) {
		t.Fatalf("Leave(user JID) err = %v, want ErrInvalidJID", err)
	}

	fc.mu.Lock()
	calls := len(fc.LeaveGroupCalls)
	fc.mu.Unlock()
	if calls != 0 {
		t.Fatalf("user-JID Leave reached the wire: %d calls", calls)
	}
}

// TestAddParticipantsRateLimit50PerDay verifies the ≤ 50 adds/day cap:
// 50 single-add calls succeed, the 51st is refused with
// app.ErrRateLimitedHard, and only 50 AuditGroupParticipantsPatch entries
// are written (FR-021 + adapter hard cap per ports_018.go).
func TestAddParticipantsRateLimit50PerDay(t *testing.T) {
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, _, a := newTestGroupAdmin(t, func() time.Time { return fixed })
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	for i := 0; i < maxParticipantAddsPerDay; i++ {
		// Every invocation uses a distinct participant so the wire call
		// stays legal; only the daily counter should trip.
		p := domain.MustJID("55119" + strconv.Itoa(10000000+i))
		if err := g.AddParticipants(ctx, group, []domain.JID{p}); err != nil {
			t.Fatalf("AddParticipants #%d = %v, want nil", i+1, err)
		}
	}

	over := domain.MustJID("5511999999999")
	err := g.AddParticipants(ctx, group, []domain.JID{over})
	if err == nil {
		t.Fatal("AddParticipants #51 returned nil, want ErrRateLimitedHard")
	}
	if !errors.Is(err, app.ErrRateLimitedHard) {
		t.Fatalf("AddParticipants #51 err = %v, want ErrRateLimitedHard", err)
	}

	var patches int
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupParticipantsPatch && e.Decision == "ok" {
			patches++
		}
	}
	if patches != maxParticipantAddsPerDay {
		t.Fatalf("AuditGroupParticipantsPatch count = %d, want %d", patches, maxParticipantAddsPerDay)
	}
}

// TestAddParticipantsRefundOnUpstreamFailure verifies that an upstream
// failure refunds the reserved Add slots so the operator can retry.
func TestAddParticipantsRefundOnUpstreamFailure(t *testing.T) {
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, fc, _ := newTestGroupAdmin(t, func() time.Time { return fixed })
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	p := domain.MustJID("5511999999999")

	fc.mu.Lock()
	fc.ParticipantsErr = errors.New("transient upstream")
	fc.mu.Unlock()

	// Spend the full 50-slot budget on failed calls.
	for i := 0; i < maxParticipantAddsPerDay; i++ {
		if err := g.AddParticipants(ctx, group, []domain.JID{p}); err == nil {
			t.Fatalf("AddParticipants #%d returned nil despite seeded err", i+1)
		}
	}

	// Clear the upstream failure; refund should have freed all 50 slots.
	fc.mu.Lock()
	fc.ParticipantsErr = nil
	fc.mu.Unlock()

	if err := g.AddParticipants(ctx, group, []domain.JID{p}); err != nil {
		t.Fatalf("AddParticipants after refund = %v, want nil", err)
	}
}

// TestRemoveParticipantsNotRateLimited verifies RemoveParticipants is NOT
// subject to the 50/day cap: an operator may always eject a spammer even
// after exhausting the Add budget.
func TestRemoveParticipantsNotRateLimited(t *testing.T) {
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, _, _ := newTestGroupAdmin(t, func() time.Time { return fixed })
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	// Burn the Add budget first.
	for i := 0; i < maxParticipantAddsPerDay; i++ {
		p := domain.MustJID("55119" + strconv.Itoa(10000000+i))
		if err := g.AddParticipants(ctx, group, []domain.JID{p}); err != nil {
			t.Fatalf("AddParticipants #%d = %v", i+1, err)
		}
	}

	// Remove must still work.
	target := domain.MustJID("5511000000001")
	if err := g.RemoveParticipants(ctx, group, []domain.JID{target}); err != nil {
		t.Fatalf("RemoveParticipants after Add cap = %v, want nil", err)
	}
}

// TestPromoteAdminOnlyRefusal verifies whatsmeow's ErrIQForbidden / 403
// from upstream surfaces as domain.ErrNotAdmin so the dispatcher can map
// it to -32100 policy_refused (FR-024).
func TestPromoteAdminOnlyRefusal(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	p := domain.MustJID("5511999999999")

	fc.mu.Lock()
	fc.ParticipantsErr = waClient.ErrIQForbidden
	fc.mu.Unlock()

	err := g.Promote(ctx, group, []domain.JID{p})
	if !errors.Is(err, domain.ErrNotAdmin) {
		t.Fatalf("Promote err = %v, want domain.ErrNotAdmin", err)
	}
}

// TestDemoteAdminOnlyRefusal verifies the same 405 mapping for Demote.
func TestDemoteAdminOnlyRefusal(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	p := domain.MustJID("5511999999999")

	fc.mu.Lock()
	fc.ParticipantsErr = waClient.ErrIQNotAllowed
	fc.mu.Unlock()

	err := g.Demote(ctx, group, []domain.JID{p})
	if !errors.Is(err, domain.ErrNotAdmin) {
		t.Fatalf("Demote err = %v, want domain.ErrNotAdmin", err)
	}
}

// TestAddParticipantsPartialFailure verifies that per-participant .Error
// codes in the upstream response surface as ErrUpstreamError listing the
// offending JIDs+codes (whatsmeow returns 200 IQ but individual participants
// fail — e.g. target privacy settings refuse the add).
func TestAddParticipantsPartialFailure(t *testing.T) {
	fixed := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	g, fc, _ := newTestGroupAdmin(t, func() time.Time { return fixed })
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	p1 := domain.MustJID("5511999999990")
	p2 := domain.MustJID("5511999999991")

	fc.mu.Lock()
	fc.ParticipantsResult = []waTypes.GroupParticipant{
		{JID: toWhatsmeow(p1), Error: 0},
		{JID: toWhatsmeow(p2), Error: 408}, // e.g. target does not allow
	}
	fc.mu.Unlock()

	err := g.AddParticipants(ctx, group, []domain.JID{p1, p2})
	if err == nil {
		t.Fatal("AddParticipants with partial-failure result returned nil, want ErrUpstreamError")
	}
	if !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("AddParticipants partial-failure err = %v, want domain.ErrUpstreamError", err)
	}
}

// TestRosterRejectsNonUserJID verifies every roster op refuses a group
// JID in the participant list (only user JIDs are valid there).
func TestRosterRejectsNonUserJID(t *testing.T) {
	g, _, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	bad := domain.MustJID("9876543210-1600000000@g.us") // another group JID

	ops := []struct {
		name string
		fn   func() error
	}{
		{"AddParticipants", func() error { return g.AddParticipants(ctx, group, []domain.JID{bad}) }},
		{"RemoveParticipants", func() error { return g.RemoveParticipants(ctx, group, []domain.JID{bad}) }},
		{"Promote", func() error { return g.Promote(ctx, group, []domain.JID{bad}) }},
		{"Demote", func() error { return g.Demote(ctx, group, []domain.JID{bad}) }},
	}
	for _, op := range ops {
		if err := op.fn(); !errors.Is(err, domain.ErrInvalidJID) {
			t.Fatalf("%s(group JID) err = %v, want domain.ErrInvalidJID", op.name, err)
		}
	}
}
