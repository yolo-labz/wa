package whatsmeow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeModHistory is a hand-rolled moderatorHistory double. It records
// every call and lets tests pre-seed GetMessageMeta return values.
type fakeModHistory struct {
	mu sync.Mutex

	// Configurable.
	MetaTS   int64
	MetaBody string
	MetaErr  error

	// Recorded.
	StampRevokeCalls []recordedStampRevoke
	StampEditCalls   []recordedStampEdit
	MetaCalls        []recordedGetMeta
}

type recordedStampRevoke struct {
	Chat      domain.JID
	ID        domain.MessageID
	Scope     domain.RevokeScope
	RevokedAt int64
}

type recordedStampEdit struct {
	Chat     domain.JID
	ID       domain.MessageID
	NewBody  string
	EditedAt int64
}

type recordedGetMeta struct {
	Chat domain.JID
	ID   domain.MessageID
}

func (h *fakeModHistory) GetMessageMeta(_ context.Context, chat domain.JID, id domain.MessageID) (int64, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.MetaCalls = append(h.MetaCalls, recordedGetMeta{Chat: chat, ID: id})
	return h.MetaTS, h.MetaBody, h.MetaErr
}

func (h *fakeModHistory) StampRevoke(_ context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope, revokedAt int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.StampRevokeCalls = append(h.StampRevokeCalls, recordedStampRevoke{Chat: chat, ID: id, Scope: scope, RevokedAt: revokedAt})
	return nil
}

func (h *fakeModHistory) StampEdit(_ context.Context, chat domain.JID, id domain.MessageID, newBody string, editedAt int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.StampEditCalls = append(h.StampEditCalls, recordedStampEdit{Chat: chat, ID: id, NewBody: newBody, EditedAt: editedAt})
	return nil
}

func newTestModerator(t *testing.T, nowFn func() time.Time) (*Moderator, *fakeWhatsmeowClient, *fakeModHistory) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	hist := &fakeModHistory{}
	m, err := NewModerator(fc, hist, newAuditRing(16), discardLogger(), nowFn)
	if err != nil {
		t.Fatalf("NewModerator: %v", err)
	}
	return m, fc, hist
}

// TestRevokeSelfLocalOnly — FR-014 scope=self touches messages.db only
// and MUST NOT call BuildRevoke / SendMessage. The ProtocolMessage REVOKE
// tombstone is reserved for scope=everyone.
func TestRevokeSelfLocalOnly(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-A", domain.RevokeSelf); err != nil {
		t.Fatalf("Revoke(self): %v", err)
	}
	if len(fc.RevokeCalls) != 0 {
		t.Errorf("BuildRevoke must not fire for scope=self; got %d", len(fc.RevokeCalls))
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("SendMessage must not fire for scope=self; got %d", len(fc.SentMessages))
	}
	if got := len(hist.StampRevokeCalls); got != 1 {
		t.Fatalf("StampRevoke: want 1 call, got %d", got)
	}
	call := hist.StampRevokeCalls[0]
	if call.Scope != domain.RevokeSelf {
		t.Errorf("scope: want self, got %s", call.Scope)
	}
	if call.RevokedAt != fixedNowFn().Unix() {
		t.Errorf("revokedAt: want %d, got %d", fixedNowFn().Unix(), call.RevokedAt)
	}
}

// TestRevokeEveryoneProtocolMessage — FR-014 scope=everyone sends a
// ProtocolMessage REVOKE tombstone (BuildRevoke + SendMessage) BEFORE
// stamping the local row. Ordering matters: a failed network call must
// not leave a locally-dead row peers still see.
func TestRevokeEveryoneProtocolMessage(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-B", domain.RevokeEveryone); err != nil {
		t.Fatalf("Revoke(everyone): %v", err)
	}
	if got := len(fc.RevokeCalls); got != 1 {
		t.Fatalf("BuildRevoke: want 1 call, got %d", got)
	}
	if fc.RevokeCalls[0].ID != "MSG-B" {
		t.Errorf("BuildRevoke id: want MSG-B, got %s", fc.RevokeCalls[0].ID)
	}
	if fc.RevokeCalls[0].Chat.String() != chat.String() {
		t.Errorf("BuildRevoke chat: want %s, got %s", chat.String(), fc.RevokeCalls[0].Chat.String())
	}
	if got := len(fc.SentMessages); got != 1 {
		t.Errorf("SendMessage: want 1 call, got %d", got)
	}
	if got := len(hist.StampRevokeCalls); got != 1 {
		t.Errorf("StampRevoke: want 1 call, got %d", got)
	}
	if hist.StampRevokeCalls[0].Scope != domain.RevokeEveryone {
		t.Errorf("scope: want everyone, got %s", hist.StampRevokeCalls[0].Scope)
	}
}

// TestEditRejectedAfter15Min — FR-015a refuses edits whose original
// timestamp is older than now-domain.EditWindow with a typed error the
// socket dispatcher routes to -32100 policy_refused. The network MUST
// NOT be hit.
func TestEditRejectedAfter15Min(t *testing.T) {
	now := fixedNowFn()
	hist := &fakeModHistory{
		// Original sent 16 minutes ago, just past the 15-min window.
		MetaTS: now.Add(-16 * time.Minute).Unix(),
	}
	fc := newFakeClient()
	fc.ConnectedFlag = true
	m, err := NewModerator(fc, hist, newAuditRing(16), discardLogger(), fixedNowFn)
	if err != nil {
		t.Fatalf("NewModerator: %v", err)
	}

	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	err = m.Edit(context.Background(), chat, "MSG-C", "corrected")
	if err == nil {
		t.Fatalf("want ErrOutsideEditWindow; got nil")
	}
	if !errors.Is(err, domain.ErrOutsideEditWindow) {
		t.Errorf("want wrapped ErrOutsideEditWindow; got %v", err)
	}
	if len(fc.EditCalls) != 0 {
		t.Errorf("BuildEdit must not fire outside the window; got %d", len(fc.EditCalls))
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("SendMessage must not fire outside the window; got %d", len(fc.SentMessages))
	}
	if len(hist.StampEditCalls) != 0 {
		t.Errorf("StampEdit must not fire outside the window; got %d", len(hist.StampEditCalls))
	}
}

// TestEditPreservesPrevious — FR-015 on-happy-path the edit is sent as a
// ProtocolMessage MESSAGE_EDIT AND StampEdit is called so previous_body
// persists. The stamped newBody must match the caller's input verbatim.
func TestEditPreservesPrevious(t *testing.T) {
	now := fixedNowFn()
	hist := &fakeModHistory{
		// Original sent 1 minute ago, comfortably inside the window.
		MetaTS:   now.Add(-1 * time.Minute).Unix(),
		MetaBody: "original body",
	}
	fc := newFakeClient()
	fc.ConnectedFlag = true
	m, err := NewModerator(fc, hist, newAuditRing(16), discardLogger(), fixedNowFn)
	if err != nil {
		t.Fatalf("NewModerator: %v", err)
	}

	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	newBody := "corrected body"
	if err := m.Edit(context.Background(), chat, "MSG-D", newBody); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got := len(fc.EditCalls); got != 1 {
		t.Fatalf("BuildEdit: want 1 call, got %d", got)
	}
	if fc.EditCalls[0].ID != "MSG-D" {
		t.Errorf("BuildEdit id: want MSG-D, got %s", fc.EditCalls[0].ID)
	}
	if got := len(fc.SentMessages); got != 1 {
		t.Errorf("SendMessage: want 1 call, got %d", got)
	}
	if got := len(hist.StampEditCalls); got != 1 {
		t.Fatalf("StampEdit: want 1 call, got %d", got)
	}
	call := hist.StampEditCalls[0]
	if call.NewBody != newBody {
		t.Errorf("StampEdit newBody: want %q, got %q", newBody, call.NewBody)
	}
	if call.ID != "MSG-D" {
		t.Errorf("StampEdit id: want MSG-D, got %s", call.ID)
	}
	if call.EditedAt != fixedNowFn().Unix() {
		t.Errorf("editedAt: want %d, got %d", fixedNowFn().Unix(), call.EditedAt)
	}
}
