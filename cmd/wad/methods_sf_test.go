package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// failAudit is an AuditLog whose Record always fails — the SF-01 fault
// injection for the allow add/remove audit-before-persist contract.
type failAudit struct{}

func (failAudit) Record(context.Context, domain.AuditEvent) error {
	return errors.New("audit sink down")
}

// okAudit records nothing and never fails.
type okAudit struct{}

func (okAudit) Record(context.Context, domain.AuditEvent) error { return nil }

func allowCall(t *testing.T, al *domain.Allowlist, mu *sync.RWMutex, path string, audit app.AuditLog, params string) (json.RawMessage, error) {
	t.Helper()
	h := handleAllow(al, mu, path, audit, slog.New(slog.DiscardHandler))
	return h(context.Background(), json.RawMessage(params))
}

// TestAllowAddAuditFailureReverts asserts SF-01: a grant whose audit row
// cannot be written is reverted and the RPC fails — never silent success.
func TestAllowAddAuditFailureReverts(t *testing.T) {
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	path := filepath.Join(t.TempDir(), "allowlist.toml")

	_, err := allowCall(t, al, &mu, path, failAudit{},
		`{"op":"add","jid":"5511999999999@s.whatsapp.net","actions":["send"]}`)
	if err == nil {
		t.Fatal("allow add with failing audit returned success; want error")
	}
	if al.Size() != 0 {
		t.Errorf("allowlist size = %d after reverted grant, want 0", al.Size())
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("allowlist file written despite reverted grant (stat err = %v)", statErr)
	}
}

// TestAllowAddAuditFailurePreservesPrior asserts the revert restores the
// exact pre-grant state instead of wiping pre-existing actions.
func TestAllowAddAuditFailurePreservesPrior(t *testing.T) {
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	jid, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	al.Grant(jid, domain.ActionSend)

	_, err = allowCall(t, al, &mu, path, failAudit{},
		`{"op":"add","jid":"5511999999999@s.whatsapp.net","actions":["read"]}`)
	if err == nil {
		t.Fatal("allow add with failing audit returned success; want error")
	}
	if !al.Allows(jid, domain.ActionSend) {
		t.Error("pre-existing send grant lost by revert")
	}
	if al.Allows(jid, domain.ActionRead) {
		t.Error("reverted read grant still present")
	}
}

// TestAllowRemoveAuditFailureReverts asserts SF-01 for the revoke path:
// a remove whose audit row cannot be written is re-granted and fails.
func TestAllowRemoveAuditFailureReverts(t *testing.T) {
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	jid, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	al.Grant(jid, domain.ActionSend, domain.ActionRead)

	_, err = allowCall(t, al, &mu, path, failAudit{},
		`{"op":"remove","jid":"5511999999999@s.whatsapp.net"}`)
	if err == nil {
		t.Fatal("allow remove with failing audit returned success; want error")
	}
	if !al.Allows(jid, domain.ActionSend) || !al.Allows(jid, domain.ActionRead) {
		t.Errorf("revoked actions not restored by revert: entries = %v", al.Entries())
	}
}

// TestAllowAddSuccessPersistsAndAudits is the happy-path control: with a
// working audit sink the grant lands in memory AND on disk.
func TestAllowAddSuccessPersistsAndAudits(t *testing.T) {
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	path := filepath.Join(t.TempDir(), "allowlist.toml")
	jid, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := allowCall(t, al, &mu, path, okAudit{},
		`{"op":"add","jid":"5511999999999@s.whatsapp.net","actions":["send"]}`)
	if err != nil {
		t.Fatalf("allow add: %v", err)
	}
	var res struct {
		Added bool `json:"added"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || !res.Added {
		t.Fatalf("allow add result = %s (unmarshal err %v), want added=true", raw, err)
	}
	if !al.Allows(jid, domain.ActionSend) {
		t.Error("grant not applied")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("allowlist file not persisted: %v", statErr)
	}
}
