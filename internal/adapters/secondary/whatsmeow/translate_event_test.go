package whatsmeow

import (
	"errors"
	"strings"
	"testing"
	"time"

	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
)

var fixedNow = time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)

func fixedNowFn() time.Time { return fixedNow }

func mustWAJID(t *testing.T, s string) waTypes.JID {
	t.Helper()
	j, err := waTypes.ParseJID(s)
	if err != nil {
		t.Fatalf("ParseJID %q: %v", s, err)
	}
	return j
}

func TestTranslate_MessageConversation(t *testing.T) {
	t.Parallel()
	sender := mustWAJID(t, "5511999990000@s.whatsapp.net")
	chat := sender
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: chat, Sender: sender},
			ID:            "ABC",
			PushName:      "Alice",
			Timestamp:     fixedNow,
		},
		Message: &waE2E.Message{Conversation: new("hello")},
	}
	got, se, _ := translateEvent(1, fixedNowFn, evt)
	if se != sideEffectNone {
		t.Fatalf("sideEffect = %v, want none", se)
	}
	me, ok := got.(domain.MessageEvent)
	if !ok {
		t.Fatalf("got %T, want MessageEvent", got)
	}
	if me.ID != "1" {
		t.Errorf("ID=%q", me.ID)
	}
	if me.PushName != "Alice" {
		t.Errorf("PushName=%q", me.PushName)
	}
	tm, ok := me.Message.(domain.TextMessage)
	if !ok {
		t.Fatalf("Message %T, want TextMessage", me.Message)
	}
	if tm.Body != "hello" {
		t.Errorf("body=%q", tm.Body)
	}
}

func TestTranslate_MessageExtendedText(t *testing.T) {
	t.Parallel()
	jid := mustWAJID(t, "5511999990000@s.whatsapp.net")
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: jid, Sender: jid},
			Timestamp:     fixedNow,
		},
		Message: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: new("linkpreview body")},
		},
	}
	got, _, _ := translateEvent(2, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	tm, ok := me.Message.(domain.TextMessage)
	if !ok || tm.Body != "linkpreview body" {
		t.Errorf("body=%v ok=%v", me.Message, ok)
	}
}

func TestTranslate_MessageReaction(t *testing.T) {
	t.Parallel()
	jid := mustWAJID(t, "5511999990000@s.whatsapp.net")
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: jid, Sender: jid},
			Timestamp:     fixedNow,
		},
		Message: &waE2E.Message{
			ReactionMessage: &waE2E.ReactionMessage{
				Key:  &waCommon.MessageKey{ID: new("TARGET1")},
				Text: new("👍"),
			},
		},
	}
	got, _, _ := translateEvent(3, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	rx, ok := me.Message.(domain.ReactionMessage)
	if !ok {
		t.Fatalf("got %T, want ReactionMessage", me.Message)
	}
	if rx.TargetID != "TARGET1" || rx.Emoji != "👍" {
		t.Errorf("reaction=%+v", rx)
	}
}

func TestTranslate_Receipt(t *testing.T) {
	t.Parallel()
	jid := mustWAJID(t, "5511999990000@s.whatsapp.net")
	evt := &events.Receipt{
		MessageSource: waTypes.MessageSource{Chat: jid, Sender: jid},
		MessageIDs:    []waTypes.MessageID{"M1"},
		Timestamp:     fixedNow,
		Type:          waTypes.ReceiptTypeRead,
	}
	got, se, _ := translateEvent(4, fixedNowFn, evt)
	if se != sideEffectNone {
		t.Fatalf("se=%v", se)
	}
	r := got.(domain.ReceiptEvent)
	if r.MessageID != "M1" || r.Status != domain.ReceiptRead {
		t.Errorf("receipt=%+v", r)
	}
}

func TestTranslate_Connected(t *testing.T) {
	t.Parallel()
	got, se, _ := translateEvent(5, fixedNowFn, &events.Connected{})
	if se != sideEffectNone {
		t.Fatalf("se=%v", se)
	}
	ce := got.(domain.ConnectionEvent)
	if ce.State != domain.ConnConnected || !ce.TS.Equal(fixedNow) {
		t.Errorf("ce=%+v", ce)
	}
}

func TestTranslate_Disconnected(t *testing.T) {
	t.Parallel()
	got, _, _ := translateEvent(6, fixedNowFn, &events.Disconnected{})
	ce := got.(domain.ConnectionEvent)
	if ce.State != domain.ConnDisconnected {
		t.Errorf("state=%v", ce.State)
	}
}

func TestTranslate_LoggedOut(t *testing.T) {
	t.Parallel()
	evt := &events.LoggedOut{OnConnect: true}
	got, se, detail := translateEvent(7, fixedNowFn, evt)
	if got != nil {
		t.Errorf("got event %v, want nil", got)
	}
	if se != sideEffectLoggedOut {
		t.Errorf("se=%v", se)
	}
	if !strings.Contains(detail, "logged out") {
		t.Errorf("detail=%q", detail)
	}
}

func TestTranslate_PairSuccess(t *testing.T) {
	t.Parallel()
	got, _, _ := translateEvent(8, fixedNowFn, &events.PairSuccess{})
	pe := got.(domain.PairingEvent)
	if pe.State != domain.PairSuccess {
		t.Errorf("state=%v", pe.State)
	}
}

func TestTranslate_PairError(t *testing.T) {
	t.Parallel()
	evt := &events.PairError{Error: errors.New("boom")}
	got, _, detail := translateEvent(9, fixedNowFn, evt)
	pe := got.(domain.PairingEvent)
	if pe.State != domain.PairFailure {
		t.Errorf("state=%v", pe.State)
	}
	if detail != "boom" {
		t.Errorf("detail=%q", detail)
	}
}

func TestTranslate_QRIgnored(t *testing.T) {
	t.Parallel()
	got, se, _ := translateEvent(10, fixedNowFn, &events.QR{Codes: []string{"x"}})
	if got != nil {
		t.Errorf("want nil event")
	}
	if se != sideEffectIgnore {
		t.Errorf("se=%v", se)
	}
}

func TestTranslate_HistorySync(t *testing.T) {
	t.Parallel()
	got, se, _ := translateEvent(11, fixedNowFn, &events.HistorySync{})
	if got != nil {
		t.Errorf("want nil event")
	}
	if se != sideEffectHistorySync {
		t.Errorf("se=%v", se)
	}
}

func TestTranslate_UnknownEvent(t *testing.T) {
	t.Parallel()
	type bogus struct{}
	got, se, detail := translateEvent(12, fixedNowFn, &bogus{})
	if got != nil {
		t.Errorf("want nil event")
	}
	if se != sideEffectUnknown {
		t.Errorf("se=%v want unknown", se)
	}
	if !strings.Contains(detail, "unknown whatsmeow event type") {
		t.Errorf("detail=%q", detail)
	}
}

func TestTranslate_MessageEmptyBody(t *testing.T) {
	t.Parallel()
	jid := mustWAJID(t, "5511999990000@s.whatsapp.net")
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: jid, Sender: jid},
			Timestamp:     fixedNow,
		},
		Message: &waE2E.Message{},
	}
	got, _, _ := translateEvent(13, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	tm := me.Message.(domain.TextMessage)
	if tm.Body != "" {
		t.Errorf("body=%q, want empty", tm.Body)
	}
}

func TestMapReceiptType(t *testing.T) {
	t.Parallel()
	cases := map[waTypes.ReceiptType]domain.ReceiptStatus{
		waTypes.ReceiptTypeDelivered: domain.ReceiptDelivered,
		waTypes.ReceiptTypeRead:      domain.ReceiptRead,
		waTypes.ReceiptTypeReadSelf:  domain.ReceiptRead,
		waTypes.ReceiptTypePlayed:    domain.ReceiptPlayed,
	}
	for in, want := range cases {
		if got := mapReceiptType(in); got != want {
			t.Errorf("mapReceiptType(%q)=%v want %v", in, got, want)
		}
	}
}
