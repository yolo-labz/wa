package whatsmeow

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// osWriteFile wraps os.WriteFile with 0o600 perms so every test tempfile
// keeps the repo's standard mode.
func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

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

	for i := range maxGroupCreatesPerDay {
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

	for i := range maxGroupCreatesPerDay {
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

	for i := range maxGroupCreatesPerDay {
		if _, err := g.Create(ctx, "D1", []domain.JID{participant}); err != nil {
			t.Fatalf("Create day1 #%d = %v", i+1, err)
		}
	}

	now = now.Add(24 * time.Hour)

	for i := range maxGroupCreatesPerDay {
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

	for i := range maxParticipantAddsPerDay {
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
	for i := range maxParticipantAddsPerDay {
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
	for i := range maxParticipantAddsPerDay {
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

// TestGroupEditEmptyPatchRefused verifies an all-nil GroupPatch is refused
// with domain.ErrEmptyGroupPatch so the dispatcher returns -32100.
func TestGroupEditEmptyPatchRefused(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	err := g.Edit(ctx, group, domain.GroupPatch{})
	if !errors.Is(err, domain.ErrEmptyGroupPatch) {
		t.Fatalf("Edit(empty patch) err = %v, want ErrEmptyGroupPatch", err)
	}

	fc.mu.Lock()
	n := len(fc.SetGroupNameCalls) + len(fc.SetGroupTopicCalls) + len(fc.SetGroupPhotoCalls)
	fc.mu.Unlock()
	if n != 0 {
		t.Fatalf("empty patch reached the wire: %d calls", n)
	}
}

// TestGroupEditSubjectOnly verifies a patch with only Subject set calls
// SetGroupName exactly once and skips Topic/Photo.
func TestGroupEditSubjectOnly(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	subject := "New Subject"

	if err := g.Edit(ctx, group, domain.GroupPatch{Subject: &subject}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	fc.mu.Lock()
	nameCalls := append([]recordedGroupName(nil), fc.SetGroupNameCalls...)
	topicCalls := len(fc.SetGroupTopicCalls)
	photoCalls := len(fc.SetGroupPhotoCalls)
	fc.mu.Unlock()
	if len(nameCalls) != 1 || nameCalls[0].Name != subject {
		t.Fatalf("SetGroupNameCalls = %+v, want 1× %q", nameCalls, subject)
	}
	if topicCalls != 0 || photoCalls != 0 {
		t.Fatalf("unexpected calls: topic=%d photo=%d", topicCalls, photoCalls)
	}

	var sawEdit bool
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupParticipantsPatch && e.Decision == "ok" && e.Detail == "edit:subject" {
			sawEdit = true
			break
		}
	}
	if !sawEdit {
		t.Fatal("missing AuditGroupParticipantsPatch[edit:subject]")
	}
}

// TestGroupEditIconRemoval verifies an empty-string IconPath passes a nil
// avatar slice to SetGroupPhoto (the WhatsApp server-side "remove" semantics).
func TestGroupEditIconRemoval(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	empty := ""

	if err := g.Edit(ctx, group, domain.GroupPatch{IconPath: &empty}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	fc.mu.Lock()
	calls := append([]recordedGroupPhoto(nil), fc.SetGroupPhotoCalls...)
	fc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("SetGroupPhotoCalls = %d, want 1", len(calls))
	}
	if calls[0].Avatar != nil {
		t.Fatalf("removal: avatar = %v, want nil", calls[0].Avatar)
	}
}

// TestGroupEditIconFromFile verifies a non-empty IconPath is read from
// disk and forwarded to SetGroupPhoto as the exact bytes.
func TestGroupEditIconFromFile(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	dir := t.TempDir()
	path := dir + "/icon.jpg"
	want := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0xAA, 0xBB, 0xCC}
	if err := osWriteFile(path, want); err != nil {
		t.Fatal(err)
	}

	if err := g.Edit(ctx, group, domain.GroupPatch{IconPath: &path}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	fc.mu.Lock()
	calls := append([]recordedGroupPhoto(nil), fc.SetGroupPhotoCalls...)
	fc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("SetGroupPhotoCalls = %d, want 1", len(calls))
	}
	if string(calls[0].Avatar) != string(want) {
		t.Fatalf("avatar bytes = %x, want %x", calls[0].Avatar, want)
	}
}

// TestGroupEditAdminOnlyRefusal verifies whatsmeow's 403 on SetGroupName
// surfaces as domain.ErrNotAdmin.
func TestGroupEditAdminOnlyRefusal(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	sub := "whatever"

	fc.mu.Lock()
	fc.SetGroupNameErr = waClient.ErrIQForbidden
	fc.mu.Unlock()

	err := g.Edit(ctx, group, domain.GroupPatch{Subject: &sub})
	if !errors.Is(err, domain.ErrNotAdmin) {
		t.Fatalf("Edit(403) err = %v, want ErrNotAdmin", err)
	}
}

// TestGroupEditAllThreeFields verifies a patch touching subject, description,
// and icon all apply in order and record one combined audit entry.
func TestGroupEditAllThreeFields(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")

	dir := t.TempDir()
	path := dir + "/icon.jpg"
	if err := osWriteFile(path, []byte{0xFF, 0xD8, 0xFF, 0x01}); err != nil {
		t.Fatal(err)
	}
	sub := "Subj"
	desc := "Desc"

	patch := domain.GroupPatch{Subject: &sub, Description: &desc, IconPath: &path}
	if err := g.Edit(ctx, group, patch); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	fc.mu.Lock()
	n := len(fc.SetGroupNameCalls) + len(fc.SetGroupTopicCalls) + len(fc.SetGroupPhotoCalls)
	fc.mu.Unlock()
	if n != 3 {
		t.Fatalf("total calls = %d, want 3", n)
	}

	var detail string
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupParticipantsPatch && e.Decision == "ok" {
			detail = e.Detail
			break
		}
	}
	if detail != "edit:subject,description,icon:set" {
		t.Fatalf("audit detail = %q, want %q", detail, "edit:subject,description,icon:set")
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

// TestInviteGetSuccess verifies the happy path — adapter returns the
// URL + derived Code (prefix stripped) and writes NO audit entry (read-only).
func TestInviteGetSuccess(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	fc.InviteLinkURL = waClient.InviteLinkPrefix + "ABC123XYZ"

	link, err := g.InviteGet(ctx, group)
	if err != nil {
		t.Fatalf("InviteGet: %v", err)
	}
	if link.URL != fc.InviteLinkURL {
		t.Fatalf("URL = %q, want %q", link.URL, fc.InviteLinkURL)
	}
	if link.Code != "ABC123XYZ" {
		t.Fatalf("Code = %q, want %q", link.Code, "ABC123XYZ")
	}
	if link.Group != group {
		t.Fatalf("Group = %v, want %v", link.Group, group)
	}
	// Confirm no reset call.
	fc.mu.Lock()
	calls := append([]recordedInviteLink(nil), fc.InviteLinkCalls...)
	fc.mu.Unlock()
	if len(calls) != 1 || calls[0].Reset {
		t.Fatalf("calls = %+v, want one call with Reset=false", calls)
	}
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupInviteJoin {
			t.Fatalf("InviteGet must not audit; got %+v", e)
		}
	}
}

// TestInviteRevokeAuditsWithNewURL verifies Reset=true, records
// AuditGroupInviteJoin with decision="revoke", and the Detail is the
// fresh URL (not the old one).
func TestInviteRevokeAuditsWithNewURL(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	group := domain.MustJID("1234567890-1600000000@g.us")
	fc.InviteLinkURL = waClient.InviteLinkPrefix + "NEWCODE"

	link, err := g.InviteRevoke(ctx, group)
	if err != nil {
		t.Fatalf("InviteRevoke: %v", err)
	}
	if link.Code != "NEWCODE" {
		t.Fatalf("Code = %q, want NEWCODE", link.Code)
	}
	fc.mu.Lock()
	calls := append([]recordedInviteLink(nil), fc.InviteLinkCalls...)
	fc.mu.Unlock()
	if len(calls) != 1 || !calls[0].Reset {
		t.Fatalf("calls = %+v, want one call with Reset=true", calls)
	}
	var seen int
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupInviteJoin && e.Decision == "revoke" {
			seen++
			if e.Detail != fc.InviteLinkURL {
				t.Fatalf("Detail = %q, want %q", e.Detail, fc.InviteLinkURL)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("AuditGroupInviteJoin(revoke) count = %d, want 1", seen)
	}
}

// TestInviteRevokedMapsToUpstreamError verifies the three "link dead"
// whatsmeow sentinels all collapse to domain.ErrUpstreamError (-32000).
func TestInviteRevokedMapsToUpstreamError(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"revoked", waClient.ErrInviteLinkRevoked},
		{"invalid", waClient.ErrInviteLinkInvalid},
		{"unauthorized", waClient.ErrGroupInviteLinkUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, fc, _ := newTestGroupAdmin(t, nil)
			fc.InviteLinkErr = tc.err
			group := domain.MustJID("1234567890-1600000000@g.us")
			_, err := g.InviteGet(context.Background(), group)
			if !errors.Is(err, domain.ErrUpstreamError) {
				t.Fatalf("err = %v, want domain.ErrUpstreamError", err)
			}
		})
	}
}

// TestInviteJoinRejectsMalformedURL verifies the regex rejects every
// URL that does not match the FR-025 pattern — bare codes, other hosts,
// http:// schemes, and codes with invalid characters.
func TestInviteJoinRejectsMalformedURL(t *testing.T) {
	g, fc, _ := newTestGroupAdmin(t, nil)
	ctx := context.Background()

	bad := []string{
		"",
		"ABC123",
		"http://chat.whatsapp.com/ABC123",
		"https://example.com/ABC123",
		"https://chat.whatsapp.com/",
		"https://chat.whatsapp.com/ABC 123",
	}
	for _, u := range bad {
		if _, err := g.InviteJoin(ctx, u); !errors.Is(err, domain.ErrInvalidJID) {
			t.Fatalf("InviteJoin(%q) err = %v, want ErrInvalidJID", u, err)
		}
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.JoinLinkCalls) != 0 {
		t.Fatalf("malformed URL must not reach upstream; got %v", fc.JoinLinkCalls)
	}
}

// TestInviteJoinHappyPath verifies the full flow: regex accepts, upstream
// succeeds, adapter translates the returned group JID, writes one
// AuditGroupInviteJoin(ok).
func TestInviteJoinHappyPath(t *testing.T) {
	g, fc, a := newTestGroupAdmin(t, nil)
	ctx := context.Background()
	fc.JoinLinkJID = waTypes.NewJID("1234567890-1600000000", waTypes.GroupServer)
	url := waClient.InviteLinkPrefix + "CODE42"

	group, err := g.InviteJoin(ctx, url)
	if err != nil {
		t.Fatalf("InviteJoin: %v", err)
	}
	want := domain.MustJID("1234567890-1600000000@g.us")
	if group.JID != want {
		t.Fatalf("JID = %v, want %v", group.JID, want)
	}
	var seen int
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditGroupInviteJoin && e.Decision == "ok" && e.Detail == url {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("AuditGroupInviteJoin(ok) count = %d, want 1", seen)
	}
}
