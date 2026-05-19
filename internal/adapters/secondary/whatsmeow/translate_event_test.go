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

	"github.com/yolo-labz/wa/v2/internal/domain"
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

// TestTranslate_MessageEmptyBody: a non-nil *waE2E.Message with every
// oneof branch empty is a genuinely unknown shape — R-05 forbids the old
// silent fallback to an empty TextMessage (CLAUDE.md rule 12).
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
	um, ok := me.Message.(domain.UnknownMessage)
	if !ok {
		t.Fatalf("want UnknownMessage, got %T", me.Message)
	}
	if um.Detail != "unknown" {
		t.Errorf("detail=%q, want %q", um.Detail, "unknown")
	}
}

// --- R-05 typed-variant translations (feature 018 T1-12) ---

// buildInbound is a tiny factory that wraps the repeated Info{Chat,Sender}
// boilerplate for the R-05 tests. Chat and sender are the same DM here —
// the sender field matters for MessageEvent.From but these tests assert
// the translated MessageEvent.Message variant.
func buildInbound(t *testing.T, msg *waE2E.Message) *events.Message {
	t.Helper()
	jid := mustWAJID(t, "5511999990000@s.whatsapp.net")
	return &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: jid, Sender: jid},
			Timestamp:     fixedNow,
		},
		Message: msg,
	}
}

func TestTranslateAudioTyped(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			DirectPath: new("/audio/blob"),
			Mimetype:   new("audio/ogg; codecs=opus"),
			Seconds:    new(uint32(5)),
			PTT:        new(true),
		},
	})
	got, _, _ := translateEvent(100, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	am, ok := me.Message.(domain.AudioMessage)
	if !ok {
		t.Fatalf("got %T, want AudioMessage", me.Message)
	}
	if am.Path != "/audio/blob" || am.Seconds != 5 || !am.PTT {
		t.Errorf("audio=%+v", am)
	}
}

func TestTranslateVideoTyped(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			DirectPath:  new("/video/blob"),
			Mimetype:    new("video/mp4"),
			Caption:     new("trip recap"),
			Seconds:     new(uint32(12)),
			GifPlayback: new(false),
		},
	})
	got, _, _ := translateEvent(101, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	vm, ok := me.Message.(domain.VideoMessage)
	if !ok {
		t.Fatalf("got %T, want VideoMessage", me.Message)
	}
	if vm.Path != "/video/blob" || vm.Caption != "trip recap" || vm.Seconds != 12 || vm.IsGif {
		t.Errorf("video=%+v", vm)
	}
}

func TestTranslateDocumentTyped(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			DirectPath: new("/doc/blob"),
			Mimetype:   new("application/pdf"),
			FileName:   new("invoice.pdf"),
			Caption:    new("q1 invoice"),
		},
	})
	got, _, _ := translateEvent(102, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	dm, ok := me.Message.(domain.DocumentMessage)
	if !ok {
		t.Fatalf("got %T, want DocumentMessage", me.Message)
	}
	if dm.Filename != "invoice.pdf" || dm.Mime != "application/pdf" || dm.Caption != "q1 invoice" {
		t.Errorf("doc=%+v", dm)
	}
}

func TestTranslateStickerTyped(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		StickerMessage: &waE2E.StickerMessage{
			DirectPath: new("/stk/blob"),
			Mimetype:   new("image/webp"),
			IsAnimated: new(true),
		},
	})
	got, _, _ := translateEvent(103, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	sm, ok := me.Message.(domain.StickerMessage)
	if !ok {
		t.Fatalf("got %T, want StickerMessage", me.Message)
	}
	if !sm.IsAnimated || sm.Mime != "image/webp" {
		t.Errorf("sticker=%+v", sm)
	}
}

func TestTranslateContactTyped(t *testing.T) {
	t.Parallel()
	vcard := "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD"
	evt := buildInbound(t, &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: new("Alice"),
			Vcard:       new(vcard),
		},
	})
	got, _, _ := translateEvent(104, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	cc, ok := me.Message.(domain.ContactCard)
	if !ok {
		t.Fatalf("got %T, want ContactCard", me.Message)
	}
	if cc.DisplayName != "Alice" || cc.VCard != vcard {
		t.Errorf("contact=%+v", cc)
	}
}

func TestTranslateLocationTyped(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  new(-23.5505),
			DegreesLongitude: new(-46.6333),
			Name:             new("Paulista"),
			Address:          new("Av. Paulista, São Paulo"),
		},
	})
	got, _, _ := translateEvent(105, fixedNowFn, evt)
	me := got.(domain.MessageEvent)
	lp, ok := me.Message.(domain.LocationPin)
	if !ok {
		t.Fatalf("got %T, want LocationPin", me.Message)
	}
	if lp.Latitude != -23.5505 || lp.Longitude != -46.6333 || lp.Name != "Paulista" {
		t.Errorf("loc=%+v", lp)
	}
}

// TestTranslateUnknownFallsBackVisible: an unmapped waE2E branch yields an
// UnknownMessage with a non-empty Detail, so downstream audit + event
// consumers see the event instead of a silent empty TextMessage flat-map.
// This is the CLAUDE.md rule 12 test ("no silent fallbacks") for R-05.
func TestTranslateUnknownFallsBackVisible(t *testing.T) {
	t.Parallel()
	evt := buildInbound(t, &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{},
	})
	got, se, _ := translateEvent(106, fixedNowFn, evt)
	if se != sideEffectNone {
		t.Fatalf("se=%v; want none (unknown Message oneof is delivered as event, not dropped)", se)
	}
	me, ok := got.(domain.MessageEvent)
	if !ok {
		t.Fatalf("got %T, want MessageEvent", got)
	}
	um, ok := me.Message.(domain.UnknownMessage)
	if !ok {
		t.Fatalf("got Message %T, want UnknownMessage", me.Message)
	}
	if um.Detail == "" {
		t.Errorf("UnknownMessage.Detail must be non-empty (no silent fallbacks)")
	}
	if !strings.Contains(um.Detail, "protocolMessage") {
		t.Errorf("Detail=%q; want protocolMessage descriptor", um.Detail)
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

// Spec 110g — table-driven cover of the six upstream health events the
// translator now surfaces as ConnectivityHealthEvent. Each case asserts
// (a) the translated event is a ConnectivityHealthEvent, (b) the State
// matches expectation, (c) when Detail must be non-empty (carries
// adapter-side numeric context) the detail string is non-empty.
func TestTranslate_HealthEvents(t *testing.T) {
	t.Parallel()
	type tc struct {
		name        string
		evt         any
		wantState   domain.ConnectivityHealthState
		needsDetail bool
	}
	cases := []tc{
		{
			name: "keepalive_timeout",
			evt: &events.KeepAliveTimeout{
				ErrorCount:  3,
				LastSuccess: fixedNow.Add(-90 * time.Second),
			},
			wantState:   domain.HealthKeepAliveTimeout,
			needsDetail: true,
		},
		{
			name:      "keepalive_restored",
			evt:       &events.KeepAliveRestored{},
			wantState: domain.HealthKeepAliveRestored,
		},
		{
			name:      "stream_replaced",
			evt:       &events.StreamReplaced{},
			wantState: domain.HealthStreamReplaced,
		},
		{
			name:      "client_outdated",
			evt:       &events.ClientOutdated{},
			wantState: domain.HealthClientOutdated,
		},
		{
			name:      "manual_login_reconnect",
			evt:       &events.ManualLoginReconnect{},
			wantState: domain.HealthManualLoginReconnect,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, se, detail := translateEvent(42, fixedNowFn, c.evt)
			if se != sideEffectNone {
				t.Fatalf("side effect: got %v want sideEffectNone", se)
			}
			he, ok := got.(domain.ConnectivityHealthEvent)
			if !ok {
				t.Fatalf("translated event type: got %T want ConnectivityHealthEvent", got)
			}
			if he.State != c.wantState {
				t.Errorf("state: got %v want %v", he.State, c.wantState)
			}
			if !he.TS.Equal(fixedNow) {
				t.Errorf("ts: got %v want %v", he.TS, fixedNow)
			}
			if c.needsDetail && detail == "" {
				t.Errorf("expected non-empty detail")
			}
		})
	}
}

// Spec 110g — ConnectFailure carries the upstream reason+message. The
// translator must thread both into Detail so operators see exactly why
// the websocket refused to come up.
func TestTranslate_ConnectFailure_Detail(t *testing.T) {
	t.Parallel()
	evt := &events.ConnectFailure{
		Reason:  events.ConnectFailureReason(401),
		Message: "logged out",
	}
	got, se, detail := translateEvent(43, fixedNowFn, evt)
	if se != sideEffectNone {
		t.Fatalf("side effect: %v", se)
	}
	he, ok := got.(domain.ConnectivityHealthEvent)
	if !ok {
		t.Fatalf("type: %T", got)
	}
	if he.State != domain.HealthConnectFailure {
		t.Errorf("state: %v", he.State)
	}
	if detail == "" || !strings.Contains(detail, "401") || !strings.Contains(detail, "logged out") {
		t.Errorf("detail missing fields: %q", detail)
	}
}

// Spec 110g — TemporaryBan must surface the upstream Expire duration so
// operators can plan the recovery window. We don't assert exact wording;
// only that Expire seconds appear in Detail.
func TestTranslate_TemporaryBan_Detail(t *testing.T) {
	t.Parallel()
	evt := &events.TemporaryBan{
		Code:   events.TempBanReason(402),
		Expire: 600 * time.Second,
	}
	got, _, detail := translateEvent(44, fixedNowFn, evt)
	he, ok := got.(domain.ConnectivityHealthEvent)
	if !ok {
		t.Fatalf("type: %T", got)
	}
	if he.State != domain.HealthTemporaryBan {
		t.Errorf("state: %v", he.State)
	}
	if !strings.Contains(detail, "600") {
		t.Errorf("detail missing expire seconds: %q", detail)
	}
}
