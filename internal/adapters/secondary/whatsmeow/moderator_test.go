package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"

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

	// Configurable — GetMessageIdentity (feature 115). IdentTS falls back
	// to MetaTS when zero so existing tests need no new setup.
	IdentSender string
	IdentFromMe bool
	IdentTS     int64
	IdentErr    error

	// Recorded.
	StampRevokeCalls []recordedStampRevoke
	StampEditCalls   []recordedStampEdit
	MetaCalls        []recordedGetMeta
	IdentCalls       []recordedGetMeta
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

func (h *fakeModHistory) GetMessageIdentity(_ context.Context, chat domain.JID, id domain.MessageID) (string, bool, int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.IdentCalls = append(h.IdentCalls, recordedGetMeta{Chat: chat, ID: id})
	ts := h.IdentTS
	if ts == 0 {
		ts = h.MetaTS
	}
	return h.IdentSender, h.IdentFromMe, ts, h.IdentErr
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

// TestRevokeSelfSendsAppStatePatch — FR-115-1..5. scope=self is a
// deleteMessageForMe app-state mutation, NOT a local-only stamp. It keeps
// the half of the old TestRevokeSelfLocalOnly that is still true (no
// tombstone ever reaches a peer) and adds the half that was missing: before
// feature 115 this method stamped the row and returned success having
// deleted nothing anywhere.
func TestRevokeSelfSendsAppStatePatch(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	msgTS := fixedNowFn().Add(-2 * time.Hour)
	hist.IdentTS = msgTS.Unix()
	hist.IdentFromMe = true
	hist.IdentSender = "5511911111111@s.whatsapp.net"

	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-A", domain.RevokeSelf); err != nil {
		t.Fatalf("Revoke(self): %v", err)
	}

	// FR-115-4: delete-for-me is invisible to peers.
	if len(fc.RevokeCalls) != 0 {
		t.Errorf("BuildRevoke must not fire for scope=self; got %d", len(fc.RevokeCalls))
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("SendMessage must not fire for scope=self; got %d", len(fc.SentMessages))
	}

	// FR-115-1: exactly one regular_high mutation carrying the action.
	if got := len(fc.AppStatePatches); got != 1 {
		t.Fatalf("SendAppState: want 1 patch, got %d", got)
	}
	patch := fc.AppStatePatches[0]
	if patch.Type != appstate.WAPatchRegularHigh {
		t.Errorf("patch type: want regular_high, got %s", patch.Type)
	}
	if got := len(patch.Mutations); got != 1 {
		t.Fatalf("mutations: want 1, got %d", got)
	}
	mut := patch.Mutations[0]
	if mut.Version != 3 {
		t.Errorf("mutation version: want 3, got %d", mut.Version)
	}
	if mut.Value.GetDeleteMessageForMeAction() == nil {
		t.Fatalf("DeleteMessageForMeAction must be set; got %+v", mut.Value)
	}

	// FR-115-2: five-part index, fromMe="1", participant collapsed to "0"
	// because the sender is the chat peer, not a third party.
	wantIndex := []string{"deleteMessageForMe", chat.String(), "MSG-A", "1", "0"}
	if !slices.Equal(mut.Index, wantIndex) {
		t.Errorf("index: want %v, got %v", wantIndex, mut.Index)
	}

	// FR-115-3: the ORIGINAL message timestamp, in milliseconds — not now.
	if got := mut.Value.GetDeleteMessageForMeAction().GetMessageTimestamp(); got != msgTS.UnixMilli() {
		t.Errorf("messageTimestamp: want %d (original, ms), got %d", msgTS.UnixMilli(), got)
	}
	// media.gc owns files on disk; this call must not also delete them.
	if mut.Value.GetDeleteMessageForMeAction().GetDeleteMedia() {
		t.Error("deleteMedia must be false")
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

// TestRevokeSelfGroupParticipantIndex — FR-115-6. In a group the fifth
// index part is the third-party sender's JID, not "0". This is the one
// place whatsmeow's BuildStar and Baileys' chatModify disagree (Baileys
// hardcodes "0"); we follow whatsmeow, and this test is what turns a live
// disagreement into a failing assertion rather than silent non-deletion.
func TestRevokeSelfGroupParticipantIndex(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	hist.IdentTS = fixedNowFn().Add(-time.Hour).Unix()
	hist.IdentFromMe = false
	hist.IdentSender = "5511922222222@s.whatsapp.net"

	chat := mustParseJIDT("120363000000000000@g.us")
	if err := m.Revoke(context.Background(), chat, "MSG-G", domain.RevokeSelf); err != nil {
		t.Fatalf("Revoke(self, group): %v", err)
	}
	if got := len(fc.AppStatePatches); got != 1 {
		t.Fatalf("SendAppState: want 1 patch, got %d", got)
	}
	wantIndex := []string{"deleteMessageForMe", chat.String(), "MSG-G", "0", "5511922222222@s.whatsapp.net"}
	if got := fc.AppStatePatches[0].Mutations[0].Index; !slices.Equal(got, wantIndex) {
		t.Errorf("group index: want %v, got %v", wantIndex, got)
	}
}

// TestRevokeSelfUnknownMessage — FR-115-7. Without the row the index
// cannot be computed, and a wrong index is accepted by the server while
// deleting nothing. Refuse loudly instead of shipping a guess.
func TestRevokeSelfUnknownMessage(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	hist.IdentErr = fmt.Errorf("sqlitehistory: %w", os.ErrNotExist)

	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	err := m.Revoke(context.Background(), chat, "MSG-MISSING", domain.RevokeSelf)
	if !errors.Is(err, domain.ErrMessageUnknown) {
		t.Fatalf("want wrapped ErrMessageUnknown; got %v", err)
	}
	if len(fc.AppStatePatches) != 0 {
		t.Errorf("no patch may be sent for an unknown message; got %d", len(fc.AppStatePatches))
	}
	if len(hist.StampRevokeCalls) != 0 {
		t.Errorf("no local stamp for an unknown message; got %d", len(hist.StampRevokeCalls))
	}
}

// TestRevokeSelfAppStateFailureDoesNotStamp — FR-115-5. Same ordering rule
// scope=everyone already obeys: a failed network call must not leave a row
// the caller believes dead.
func TestRevokeSelfAppStateFailureDoesNotStamp(t *testing.T) {
	m, fc, hist := newTestModerator(t, fixedNowFn)
	fc.AppStateErr = errors.New("upstream refused the patch")
	hist.IdentTS = fixedNowFn().Add(-time.Hour).Unix()

	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-E", domain.RevokeSelf); err == nil {
		t.Fatal("want the SendAppState error to propagate; got nil")
	}
	if len(hist.StampRevokeCalls) != 0 {
		t.Errorf("StampRevoke must not fire after a failed patch; got %d", len(hist.StampRevokeCalls))
	}
}

// TestRevokeSelfDisconnected — FR-115-8. scope=self is a network call now,
// so it refuses when the socket is down instead of pretending to succeed.
func TestRevokeSelfDisconnected(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = false
	hist := &fakeModHistory{}
	m, err := NewModerator(fc, hist, newAuditRing(16), discardLogger(), fixedNowFn)
	if err != nil {
		t.Fatalf("NewModerator: %v", err)
	}
	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-F", domain.RevokeSelf); !errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("want ErrDisconnected; got %v", err)
	}
	if len(fc.AppStatePatches) != 0 {
		t.Errorf("no patch may be sent while disconnected; got %d", len(fc.AppStatePatches))
	}
	if len(hist.StampRevokeCalls) != 0 {
		t.Errorf("no local stamp while disconnected; got %d", len(hist.StampRevokeCalls))
	}
}

// TestRevokeEveryoneSendsNoAppStatePatch — FR-115-9. The everyone path is
// untouched by feature 115: tombstone, then stamp, and no app state.
func TestRevokeEveryoneSendsNoAppStatePatch(t *testing.T) {
	m, fc, _ := newTestModerator(t, fixedNowFn)
	chat := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := m.Revoke(context.Background(), chat, "MSG-H", domain.RevokeEveryone); err != nil {
		t.Fatalf("Revoke(everyone): %v", err)
	}
	if len(fc.AppStatePatches) != 0 {
		t.Errorf("scope=everyone must not push app state; got %d", len(fc.AppStatePatches))
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
