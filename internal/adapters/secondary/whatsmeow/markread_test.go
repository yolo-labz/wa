package whatsmeow

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func mustParseJIDT(s string) domain.JID {
	j, err := domain.Parse(s)
	if err != nil {
		panic("mustParseJIDT: " + err.Error())
	}
	return j
}

// stubHistory satisfies historyContainer just for markread tests: every
// method except GetSender is a no-op. MarkRead never reads anything else.
type stubHistory struct {
	senders map[string]string // messageID -> sender JID string
	err     error
}

func (s *stubHistory) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	return nil, nil
}

func (s *stubHistory) InsertDomainMessages(ctx context.Context, msgs []domain.Message) error {
	return nil
}

func (s *stubHistory) InsertRaw(ctx context.Context, chatJID, senderJID, messageID string, ts int64, body, mediaType, caption, pushName string, isFromMe bool, rawProto []byte, senderAltJID, addressingMode string) error {
	return nil
}

func (s *stubHistory) GetRawProto(ctx context.Context, messageID string) (string, []byte, error) {
	return "", nil, os.ErrNotExist
}

func (s *stubHistory) GetSender(ctx context.Context, messageID string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.senders[messageID]; ok {
		return v, nil
	}
	return "", os.ErrNotExist
}

func (s *stubHistory) Search(ctx context.Context, query string, limit int) ([]domain.Message, error) {
	return nil, nil
}
func (s *stubHistory) Close() error { return nil }

// TestMarkReadDistinctChatSender: for a 1:1 DM the sender slot must be
// populated with the same JID as chat (whatsmeow skips the participant
// attr server-side for user servers, but the R-09 fix still passes
// distinct arguments). The previous code passed (waChat, waChat) by
// accident, which silently worked for DMs but broke groups — this test
// pins the DM branch of the fix.
func TestMarkReadDistinctChatSender(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	// history not required for DMs, but set it to ensure we don't fall
	// into the "history unavailable" branch.
	a.history = &stubHistory{senders: map[string]string{}}

	dm := mustParseJIDT("5511911111111@s.whatsapp.net")
	if err := a.MarkRead(context.Background(), dm, "MSG-A"); err != nil {
		t.Fatalf("MarkRead DM: %v", err)
	}

	if got := len(fc.MarkReadCalls); got != 1 {
		t.Fatalf("want 1 MarkRead call, got %d", got)
	}
	call := fc.MarkReadCalls[0]
	if call.Chat.String() != dm.String() {
		t.Errorf("chat: want %s, got %s", dm.String(), call.Chat.String())
	}
	if call.Sender.String() != dm.String() {
		t.Errorf("sender (DM): want %s, got %s", dm.String(), call.Sender.String())
	}
	if len(call.IDs) != 1 || call.IDs[0] != "MSG-A" {
		t.Errorf("ids: want [MSG-A], got %v", call.IDs)
	}
	if call.TS.IsZero() {
		t.Errorf("timestamp must be non-zero")
	}
}

// TestMarkReadGroupSenderIsMember: for a group chat, the sender slot
// must be the participant JID (the authoring member), not the group JID.
// This is the core R-09 bug: sender == chat silently set participant to
// the group itself, making the receipt invalid.
func TestMarkReadGroupSenderIsMember(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)

	member := "5511922222222@s.whatsapp.net"
	group := mustParseJIDT("120363000000000000@g.us")
	a.history = &stubHistory{senders: map[string]string{"MSG-B": member}}

	if err := a.MarkRead(context.Background(), group, "MSG-B"); err != nil {
		t.Fatalf("MarkRead group: %v", err)
	}

	if got := len(fc.MarkReadCalls); got != 1 {
		t.Fatalf("want 1 MarkRead call, got %d", got)
	}
	call := fc.MarkReadCalls[0]
	if call.Chat.String() != group.String() {
		t.Errorf("chat: want %s, got %s", group.String(), call.Chat.String())
	}
	if call.Sender.String() != member {
		t.Errorf("sender (group): want %s, got %s", member, call.Sender.String())
	}
	if call.Sender.String() == call.Chat.String() {
		t.Errorf("sender must differ from chat in group receipts")
	}
}

// TestMarkReadGroupRequiresHistory: a group receipt with no history
// attached is a hard error — the bug surface is too severe to fall back
// to any behaviour that might silently succeed.
func TestMarkReadGroupRequiresHistory(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	// a.history remains nil.

	group := mustParseJIDT("120363000000000000@g.us")
	err := a.MarkRead(context.Background(), group, "MSG-C")
	if err == nil {
		t.Fatalf("want error; got nil")
	}
	if !strings.Contains(err.Error(), "history unavailable") {
		t.Errorf("want error about history unavailable, got: %v", err)
	}
	if len(fc.MarkReadCalls) != 0 {
		t.Errorf("must not invoke client.MarkRead on lookup failure")
	}
}

// TestMarkReadGroupUnknownMessage: when the message is absent from the
// local history store, the receipt is refused (we would otherwise emit a
// zero-JID participant and silently no-op on whatsmeow's side).
func TestMarkReadGroupUnknownMessage(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	a.history = &stubHistory{senders: map[string]string{}}

	group := mustParseJIDT("120363000000000000@g.us")
	err := a.MarkRead(context.Background(), group, "MSG-D")
	if err == nil {
		t.Fatalf("want error; got nil")
	}
	if !strings.Contains(err.Error(), "sender unknown") {
		t.Errorf("want sender-unknown error, got: %v", err)
	}
	if len(fc.MarkReadCalls) != 0 {
		t.Errorf("must not invoke client.MarkRead when sender is unknown")
	}
}

// TestMarkReadGroupHistoryError: arbitrary GetSender errors propagate
// with the markReadErr prefix and do NOT invoke the whatsmeow client.
func TestMarkReadGroupHistoryError(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	boom := errors.New("sqlite died")
	a.history = &stubHistory{err: boom}

	group := mustParseJIDT("120363000000000000@g.us")
	err := a.MarkRead(context.Background(), group, "MSG-E")
	if err == nil {
		t.Fatalf("want error; got nil")
	}
	if !strings.Contains(err.Error(), "sqlite died") {
		t.Errorf("want wrapped history error, got: %v", err)
	}
	if len(fc.MarkReadCalls) != 0 {
		t.Errorf("must not invoke client.MarkRead on history error")
	}
}

// TestMarkReadRejectsZeroJID: argument validation stays intact.
func TestMarkReadRejectsZeroJID(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	err := a.MarkRead(context.Background(), domain.JID{}, "MSG-F")
	if err == nil {
		t.Fatalf("want error for zero JID; got nil")
	}
}

// TestMarkReadRejectsEmptyID: argument validation stays intact.
func TestMarkReadRejectsEmptyID(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	dm := mustParseJIDT("5511911111111@s.whatsapp.net")
	err := a.MarkRead(context.Background(), dm, "")
	if err == nil {
		t.Fatalf("want error for empty id; got nil")
	}
}

// TestIsUserServer pins the server classification used by the MarkRead
// sender resolver. If whatsmeow ever adds another user-style server we
// need to know here, not by discovering silent no-ops in prod.
func TestIsUserServer(t *testing.T) {
	cases := []struct {
		server string
		want   bool
	}{
		{waTypes.DefaultUserServer, true},
		{waTypes.HiddenUserServer, true},
		{waTypes.MessengerServer, true},
		{waTypes.GroupServer, false},
		{waTypes.BroadcastServer, false},
		{"", false},
	}
	for _, c := range cases {
		if got := isUserServer(c.server); got != c.want {
			t.Errorf("isUserServer(%q) = %v; want %v", c.server, got, c.want)
		}
	}
}
