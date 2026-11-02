package app_test

// Regression pins for the audit trail of a REFUSED safety check (h104,
// 02/11/2026).
//
// The incident: a `message.revoke` was refused by the allowlist, and
// audit.log recorded it as `action:"send"` because the safety gate's audit
// recorder hardcoded AuditSend. The operator then chased the send path for
// two sessions while the real gate was the chat's allowlist row missing the
// `revoke` action. A forensic log that names the wrong action is worse than
// none, so the refused action is now carried through domain.AuditForAction.
//
// What each pin buys:
//   1. no grant          -> refused, and audited AS REVOKE (the regression)
//   2. `revoke` granted  -> reaches the moderator (the grant is sufficient)
//   3. `send` only       -> still refused (send must never imply revoke)
//   4. moderator errors  -> audited "error" under revoke, error surfaces
//                           (the too-old-to-revoke / window path)

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const revokeTestChat = "215178096468120@lid"
const revokeTestMsg = "3EB0AA896AA2A9115790DE"

// stubModerator records the revoke call and returns a chosen error, so a test
// can tell "the gate let it through" apart from "the server refused it".
type stubModerator struct {
	calls  int
	scopes []domain.RevokeScope
	err    error
}

func (m *stubModerator) Revoke(
	_ context.Context, _ domain.JID, _ domain.MessageID, scope domain.RevokeScope,
) error {
	m.calls++
	m.scopes = append(m.scopes, scope)
	return m.err
}

func (m *stubModerator) Edit(context.Context, domain.JID, domain.MessageID, string) error {
	return nil
}

// newRevokeDispatcher wires a dispatcher whose moderator is under test control.
func newRevokeDispatcher(t *testing.T) (*app.Dispatcher, *memory.Adapter, *stubModerator) {
	t.Helper()
	adapter := memory.New(nil)
	mod := &stubModerator{}
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		Quoted:         adapter,
		Moderator:      mod,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter, mod
}

func revokeRequest(scope string) []byte {
	return []byte(`{"chat":"` + revokeTestChat + `","messageId":"` + revokeTestMsg +
		`","scope":"` + scope + `"}`)
}

func revokedAudits(entries []domain.AuditEvent) []domain.AuditEvent {
	var out []domain.AuditEvent
	for _, e := range entries {
		if e.Action == domain.AuditRevoke {
			out = append(out, e)
		}
	}
	return out
}

func TestRevokeDeniedWithoutGrantIsAuditedAsRevoke(t *testing.T) {
	d, adapter, mod := newRevokeDispatcher(t)

	_, err := d.Handle(context.Background(), "message.revoke", revokeRequest("everyone"))
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}
	if mod.calls != 0 {
		t.Fatalf("moderator calls = %d, want 0 — a refused call must not reach the wire", mod.calls)
	}

	// THE REGRESSION: before the fix this row carried Action=AuditSend and a
	// grep for revoke denials found nothing, so the log "proved" the wrong path.
	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want exactly 1", len(entries))
	}
	got := entries[0]
	if got.Action != domain.AuditRevoke {
		t.Fatalf("audit action = %v, want AuditRevoke (a refused revoke logged as send is "+
			"the 02/11 misdiagnosis)", got.Action)
	}
	if got.Decision != "denied:allowlist" {
		t.Errorf("decision = %q, want denied:allowlist", got.Decision)
	}
	if got.Detail != "action=revoke" {
		t.Errorf("detail = %q, want action=revoke", got.Detail)
	}
}

func TestRevokeGrantedReachesTheModerator(t *testing.T) {
	d, adapter, mod := newRevokeDispatcher(t)
	adapter.Grant(domain.MustJID(revokeTestChat), domain.ActionRevoke)

	if _, err := d.Handle(context.Background(), "message.revoke", revokeRequest("everyone")); err != nil {
		t.Fatalf("Handle(message.revoke) with the revoke grant: %v", err)
	}
	if mod.calls != 1 {
		t.Fatalf("moderator calls = %d, want 1", mod.calls)
	}
	if len(mod.scopes) != 1 || mod.scopes[0] != domain.RevokeEveryone {
		t.Fatalf("scope = %v, want [everyone]", mod.scopes)
	}
	entries := revokedAudits(adapter.AuditEntries())
	if len(entries) != 1 || entries[0].Decision != "ok" {
		t.Fatalf("revoke audits = %+v, want one ok entry", entries)
	}
	if entries[0].Detail != revokeTestMsg+":everyone" {
		t.Errorf("detail = %q, want %q", entries[0].Detail, revokeTestMsg+":everyone")
	}
}

// TestRevokeSendGrantNeverImpliesRevoke is the "a send-only token must never
// gain revoke" rule at the allowlist layer: the row that lets this chat receive
// messages is not the row that lets their copies be deleted.
func TestRevokeSendGrantNeverImpliesRevoke(t *testing.T) {
	d, adapter, mod := newRevokeDispatcher(t)
	adapter.Grant(domain.MustJID(revokeTestChat), domain.ActionSend, domain.ActionRead)

	_, err := d.Handle(context.Background(), "message.revoke", revokeRequest("everyone"))
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}
	if mod.calls != 0 {
		t.Fatalf("moderator calls = %d, want 0", mod.calls)
	}
}

// TestRevokeServerRefusalIsAuditedAsError covers the window/too-old path: the
// allowlist let it through, WhatsApp refused the tombstone, and the failure has
// to be visible as an error under the revoke action rather than swallowed.
func TestRevokeServerRefusalIsAuditedAsError(t *testing.T) {
	d, adapter, mod := newRevokeDispatcher(t)
	adapter.Grant(domain.MustJID(revokeTestChat), domain.ActionRevoke)
	mod.err = errors.New("server returned error updating app state: 409 conflict")

	_, err := d.Handle(context.Background(), "message.revoke", revokeRequest("everyone"))
	if err == nil {
		t.Fatal("Handle(message.revoke) = nil error, want the server refusal surfaced")
	}
	entries := revokedAudits(adapter.AuditEntries())
	if len(entries) != 1 {
		t.Fatalf("revoke audits = %+v, want exactly 1", entries)
	}
	if entries[0].Decision != "error" {
		t.Errorf("decision = %q, want error", entries[0].Decision)
	}
	if entries[0].Detail == "" {
		t.Error("detail is empty — the server refusal text must be kept for forensics")
	}
}

// TestRevokeScopeIsValidatedBeforeTheWire pins that an unknown scope is refused
// as invalid params, not defaulted to "everyone" (an irreversible act).
func TestRevokeScopeIsValidatedBeforeTheWire(t *testing.T) {
	d, _, mod := newRevokeDispatcher(t)
	if _, err := d.Handle(context.Background(), "message.revoke", revokeRequest("EVERYONE")); err == nil {
		t.Fatal("Handle accepted scope=EVERYONE; want invalid params")
	}
	if mod.calls != 0 {
		t.Fatalf("moderator calls = %d, want 0", mod.calls)
	}
}
