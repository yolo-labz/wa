package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestAuditErrDetailSanitizes pins SEC-04: audit detail carries only
// the typed error class — never the raw upstream string, which can
// embed message bodies and recipients in an unrotated append-only log.
func TestAuditErrDetailSanitizes(t *testing.T) {
	t.Parallel()
	leaky := errors.New(`server says: failed to send "meet me at Rua X 123" to 5511999999999`)
	if got := auditErrDetail(leaky); got != "internal" {
		t.Fatalf("untyped error detail = %q, want internal", got)
	}
	if strings.Contains(auditErrDetail(leaky), "Rua X") {
		t.Fatal("raw error text leaked into audit detail")
	}
	if got := auditErrDetail(ErrNotAllowlisted); got != "code=-32012" {
		t.Fatalf("typed error detail = %q, want code=-32012", got)
	}
}

// denyAll is an Allowlist refusing everything (default-deny posture).
type denyAll struct{}

func (denyAll) Allows(domain.JID, domain.Action) bool { return false }

// allowSendOnly permits only ActionSend — proves group.add is gated
// SEPARATELY from send (SEC-06's latent default-allow).
type allowSendOnly struct{}

func (allowSendOnly) Allows(_ domain.JID, a domain.Action) bool { return a == domain.ActionSend }

// recordingGroupAdmin counts calls so tests can assert the gate fired
// BEFORE the adapter.
type recordingGroupAdmin struct {
	creates int
	adds    int
}

func (g *recordingGroupAdmin) Create(_ context.Context, subject string, p []domain.JID) (domain.Group, error) {
	g.creates++
	return domain.Group{Subject: subject, Participants: p}, nil
}
func (g *recordingGroupAdmin) Leave(context.Context, domain.JID) error { return nil }
func (g *recordingGroupAdmin) AddParticipants(context.Context, domain.JID, []domain.JID) error {
	g.adds++
	return nil
}

func (g *recordingGroupAdmin) RemoveParticipants(context.Context, domain.JID, []domain.JID) error {
	return nil
}

func (g *recordingGroupAdmin) Promote(context.Context, domain.JID, []domain.JID) error { return nil }

func (g *recordingGroupAdmin) Demote(context.Context, domain.JID, []domain.JID) error { return nil }

func (g *recordingGroupAdmin) Edit(context.Context, domain.JID, domain.GroupPatch) error {
	return nil
}

func (g *recordingGroupAdmin) InviteGet(context.Context, domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, nil
}

func (g *recordingGroupAdmin) InviteRevoke(context.Context, domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, nil
}

func (g *recordingGroupAdmin) InviteJoin(context.Context, string) (domain.Group, error) {
	return domain.Group{}, nil
}

// TestGroupOpsEnforceAllowlist pins SEC-06: group.create and
// group.addParticipants refuse participants not allowlisted for
// group.add, and the adapter is never reached on denial.
func TestGroupOpsEnforceAllowlist(t *testing.T) {
	t.Parallel()
	ga := &recordingGroupAdmin{}
	d := &Dispatcher{
		groupAdmin: ga,
		safety:     NewSafetyPipeline(allowSendOnly{}, nil),
		audit:      &recordingAudit{},
		profile:    "p",
	}

	createRaw := json.RawMessage(`{"subject":"x","participants":["5511999999999"]}`)
	if _, err := d.doGroupCreate(context.Background(), createRaw); !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("group.create with send-only allowlist: err=%v, want ErrNotAllowlisted", err)
	}
	if ga.creates != 0 {
		t.Fatal("adapter Create reached despite allowlist denial")
	}

	addRaw := json.RawMessage(`{"group":"123@g.us","participants":["5511999999999"]}`)
	if _, err := d.handleGroupAddParticipants(context.Background(), addRaw); !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("group.addParticipants: err=%v, want ErrNotAllowlisted", err)
	}
	if ga.adds != 0 {
		t.Fatal("adapter AddParticipants reached despite allowlist denial")
	}
}
