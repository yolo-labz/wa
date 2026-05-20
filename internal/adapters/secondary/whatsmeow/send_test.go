package whatsmeow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	waClient "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// waE2EVariants bundles the four typed media sub-messages so table tests
// can inspect whichever one the MIME discriminator produced.
type waE2EVariants struct {
	Image    *waE2E.ImageMessage
	Video    *waE2E.VideoMessage
	Audio    *waE2E.AudioMessage
	Document *waE2E.DocumentMessage
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newTestAdapter(t *testing.T, fc *fakeWhatsmeowClient) *Adapter {
	t.Helper()
	return openWithClient(fc, domain.NewAllowlist(), discardLogger(), fixedNowFn)
}

func TestSend_ValidationFailsBeforeIO(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	// Zero-recipient TextMessage fails Validate().
	_, err := a.Send(context.Background(), domain.TextMessage{Body: "hi"})
	if err == nil {
		t.Fatal("expected validation error; got nil")
	}
	if !errors.Is(err, domain.ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("Send should not reach whatsmeow on validation failure; got %d calls", len(fc.SentMessages))
	}
}

func TestSend_DisconnectedReturnsErrDisconnected(t *testing.T) {
	fc := newFakeClient() // ConnectedFlag=false by default
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
	if !errors.Is(err, domain.ErrDisconnected) {
		t.Errorf("want ErrDisconnected; got %v", err)
	}
}

func TestSend_SuccessReturnsMessageIDAndRecordsAudit(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.ABCDEF"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.ABCDEF" {
		t.Errorf("want wamid.ABCDEF; got %q", id)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage call; got %d", len(fc.SentMessages))
	}
	if got := a.auditBuf.Len(); got != 1 {
		t.Errorf("want 1 audit entry; got %d", got)
	}
}

func TestSend_CtxCancelledBeforeIO(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(ctx, domain.TextMessage{Recipient: to, Body: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("Send must not hit whatsmeow after ctx cancel; got %d calls", len(fc.SentMessages))
	}
}

func TestSend_MediaMessageMissingPath(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Path:      "/nonexistent/path/to/file.jpg",
		Mime:      "image/jpeg",
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist; got %v", err)
	}
}

// TestSend_MediaMessageImageUploadsAndSends — T1-08 / R-01 happy path:
// an image routes through Upload, builds an ImageMessage proto, and
// SendMessage is invoked exactly once carrying the typed variant.
func TestSend_MediaMessageImageUploadsAndSends(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.IMG1"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	tmp := filepath.Join(t.TempDir(), "img.jpg")
	if err := os.WriteFile(tmp, []byte("\xff\xd8\xff\xe0fake-jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	to := domain.MustJID("15551234567@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Path:      tmp,
		Mime:      "image/jpeg",
		Caption:   "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.IMG1" {
		t.Errorf("want wamid.IMG1; got %q", id)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	msg := fc.SentMessages[0].Msg
	if msg.GetImageMessage() == nil {
		t.Fatalf("expected ImageMessage, got %+v", msg)
	}
	if got := msg.GetImageMessage().GetMimetype(); got != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", got)
	}
	if got := msg.GetImageMessage().GetCaption(); got != "hello" {
		t.Errorf("caption = %q, want hello", got)
	}
}

// TestSend_MediaMessageVariantsByMime — T1-08: each of video/audio/document
// routes to its typed waE2E variant. Table-driven to keep the MIME
// discriminator honest.
func TestSend_MediaMessageVariantsByMime(t *testing.T) {
	cases := []struct {
		mime   string
		verify func(t *testing.T, m *waE2EVariants)
	}{
		{"video/mp4", func(t *testing.T, v *waE2EVariants) {
			if v.Video == nil {
				t.Fatalf("want VideoMessage")
			}
		}},
		{"audio/ogg", func(t *testing.T, v *waE2EVariants) {
			if v.Audio == nil {
				t.Fatalf("want AudioMessage")
			}
		}},
		{"application/pdf", func(t *testing.T, v *waE2EVariants) {
			if v.Document == nil {
				t.Fatalf("want DocumentMessage")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			fc := newFakeClient()
			fc.ConnectedFlag = true
			fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.X"), Timestamp: fixedNowFn()}
			a := newTestAdapter(t, fc)
			t.Cleanup(func() { _ = a.Close() })

			tmp := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(tmp, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			to := domain.MustJID("15551234567@s.whatsapp.net")
			if _, err := a.Send(context.Background(), domain.MediaMessage{
				Recipient: to, Path: tmp, Mime: tc.mime,
			}); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(fc.SentMessages) != 1 {
				t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
			}
			msg := fc.SentMessages[0].Msg
			tc.verify(t, &waE2EVariants{
				Image:    msg.GetImageMessage(),
				Video:    msg.GetVideoMessage(),
				Audio:    msg.GetAudioMessage(),
				Document: msg.GetDocumentMessage(),
			})
		})
	}
}

// TestReactionRoundTrip — T1-09 / R-02 happy path: a ReactionMessage
// routes through buildReactionMessage, builds a typed ReactionMessage
// proto, and SendMessage receives the expected Key/Text pair.
func TestReactionRoundTrip(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.RX1"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	targetID := domain.MessageID("wamid.TARGET42")
	id, err := a.Send(context.Background(), domain.ReactionMessage{
		Recipient: chat,
		TargetID:  targetID,
		Emoji:     "👍",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.RX1" {
		t.Errorf("want wamid.RX1; got %q", id)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	react := fc.SentMessages[0].Msg.GetReactionMessage()
	if react == nil {
		t.Fatalf("want ReactionMessage proto, got %+v", fc.SentMessages[0].Msg)
	}
	if got := react.GetText(); got != "👍" {
		t.Errorf("text = %q, want 👍", got)
	}
	key := react.GetKey()
	if key == nil {
		t.Fatalf("want non-nil Key")
	}
	if got := key.GetRemoteJID(); got != chat.String() {
		t.Errorf("key.remoteJID = %q, want %s", got, chat.String())
	}
	if got := key.GetID(); got != string(targetID) {
		t.Errorf("key.id = %q, want %s", got, targetID)
	}
	if !key.GetFromMe() {
		t.Errorf("key.fromMe = false, want true (v0 assumption)")
	}
	if react.GetSenderTimestampMS() == 0 {
		t.Errorf("senderTimestampMs = 0, want non-zero")
	}
}

// TestReactionEmptyEmojiRemoves — T1-09 / R-02: empty Emoji is the WA
// protocol sentinel for "remove the reaction" and must round-trip as
// Text="" (not dropped, not nil). Domain Validate() already permits it.
func TestReactionEmptyEmojiRemoves(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.RX2"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	targetID := domain.MessageID("wamid.TARGET99")
	if _, err := a.Send(context.Background(), domain.ReactionMessage{
		Recipient: chat,
		TargetID:  targetID,
		Emoji:     "",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	react := fc.SentMessages[0].Msg.GetReactionMessage()
	if react == nil {
		t.Fatalf("want ReactionMessage proto")
	}
	// GetText() on nil *string returns "" so assert the pointer is set
	// explicitly — the empty-string sentinel must not be dropped.
	if react.Text == nil {
		t.Fatalf("want Text pointer set to empty string, got nil (remove sentinel would be lost)")
	}
	if *react.Text != "" {
		t.Errorf("text = %q, want empty string (WA remove sentinel)", *react.Text)
	}
}

// TestBuildListResponseMessage — spec 110j FR-003 (#161 + #163 amendments):
// ListReplyMessage maps to *waE2E.ListResponseMessage with the
// SelectedRowID + Title populated, ListType = SINGLE_SELECT, and
// ContextInfo carrying StanzaID + Participant + the decoded
// QuotedMessage (required by the WhatsApp wire to avoid server
// error 479, see #163).
func TestBuildListResponseMessage(t *testing.T) {
	t.Parallel()
	sender := domain.MustJID("558134658209@s.whatsapp.net")
	// Build a real ListMessage as the "original" and marshal it so the
	// builder can round-trip it through ContextInfo.QuotedMessage.
	listTitle := "Original list"
	orig := &waE2E.Message{
		ListMessage: &waE2E.ListMessage{
			Title: &listTitle,
		},
	}
	origRaw, err := proto.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}
	msg := domain.ListReplyMessage{
		Recipient:        sender,
		RowID:            "row-7",
		Title:            "Atendente Humano",
		ContextStanzaID:  "3EBB85B4313489CE97",
		ContextSender:    sender,
		ContextQuotedRaw: origRaw,
	}
	out := buildListResponseMessage(msg, sender)
	lr := out.GetListResponseMessage()
	if lr == nil {
		t.Fatal("ListResponseMessage missing")
	}
	if got := lr.GetTitle(); got != "Atendente Humano" {
		t.Errorf("title = %q, want %q", got, "Atendente Humano")
	}
	if got := lr.GetListType(); got != waE2E.ListResponseMessage_SINGLE_SELECT {
		t.Errorf("listType = %v, want SINGLE_SELECT", got)
	}
	sel := lr.GetSingleSelectReply()
	if sel == nil {
		t.Fatal("SingleSelectReply missing")
	}
	if got := sel.GetSelectedRowID(); got != "row-7" {
		t.Errorf("rowID = %q, want %q", got, "row-7")
	}
	ctxInfo := lr.GetContextInfo()
	if ctxInfo == nil {
		t.Fatal("ContextInfo missing — WhatsApp server returns error 479 without it (#161)")
	}
	if got := ctxInfo.GetStanzaID(); got != "3EBB85B4313489CE97" {
		t.Errorf("ContextInfo.StanzaID = %q, want %q", got, "3EBB85B4313489CE97")
	}
	if got := ctxInfo.GetParticipant(); got != sender.String() {
		t.Errorf("ContextInfo.Participant = %q, want %q", got, sender.String())
	}
	// #165: RemoteJID echoes the chat JID. Baileys + WhatsApp-Web-MD
	// clients populate this; smoke against business IVRs returned error
	// 479 without it even when StanzaID + Participant + QuotedMessage
	// were all populated.
	if got := ctxInfo.GetRemoteJID(); got != sender.String() {
		t.Errorf("ContextInfo.RemoteJID = %q, want %q", got, sender.String())
	}
	// #163: QuotedMessage must round-trip the original ListMessage.
	quoted := ctxInfo.GetQuotedMessage()
	if quoted == nil {
		t.Fatal("ContextInfo.QuotedMessage missing — WhatsApp server returns error 479 without it (#163)")
	}
	if got := quoted.GetListMessage().GetTitle(); got != listTitle {
		t.Errorf("QuotedMessage.ListMessage.Title = %q, want %q", got, listTitle)
	}
}

// TestBuildListResponseMessage_NoQuotedRaw — #163: builder is pure; when
// ContextQuotedRaw is nil the ContextInfo still carries StanzaID +
// Participant but QuotedMessage stays nil. The dispatcher's no-store
// fallback ships this shape so the wire-side failure (error 479) surfaces
// instead of a silent build-side panic.
func TestBuildListResponseMessage_NoQuotedRaw(t *testing.T) {
	t.Parallel()
	sender := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ListReplyMessage{
		Recipient:       sender,
		RowID:           "row-7",
		Title:           "Atendente",
		ContextStanzaID: "stanza-1",
		ContextSender:   sender,
		// ContextQuotedRaw intentionally nil
	}
	out := buildListResponseMessage(msg, sender)
	ctxInfo := out.GetListResponseMessage().GetContextInfo()
	if ctxInfo == nil {
		t.Fatal("ContextInfo missing")
	}
	if ctxInfo.GetStanzaID() != "stanza-1" {
		t.Errorf("StanzaID = %q, want stanza-1", ctxInfo.GetStanzaID())
	}
	if ctxInfo.GetQuotedMessage() != nil {
		t.Error("QuotedMessage must be nil when ContextQuotedRaw is empty (no silent fallback)")
	}
}

// TestBuildContextInfo_CorruptRaw — #163: protobuf decode failure on the
// stored raw_proto must not crash the builder. The function is pure: a
// corrupt blob produces a ContextInfo with nil QuotedMessage so the wire
// surfaces the error, not the build site.
func TestBuildContextInfo_CorruptRaw(t *testing.T) {
	t.Parallel()
	sender := domain.MustJID("558134658209@s.whatsapp.net")
	// Random bytes that are not a valid waE2E.Message protobuf.
	corrupt := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	ctxInfo := buildContextInfo("stanza-1", sender, corrupt, sender)
	if ctxInfo == nil {
		t.Fatal("ContextInfo must be non-nil even with corrupt raw")
	}
	if ctxInfo.GetStanzaID() != "stanza-1" {
		t.Errorf("StanzaID = %q, want stanza-1", ctxInfo.GetStanzaID())
	}
	if ctxInfo.GetQuotedMessage() != nil {
		t.Error("QuotedMessage must stay nil when raw_proto decode fails (let the wire reject)")
	}
}

// TestBuildButtonReplyMessage_Buttons — spec 110j FR-003 (#161 amendment):
// Kind=Buttons maps to *waE2E.ButtonsResponseMessage with Type = DISPLAY_TEXT
// and a populated ContextInfo block.
func TestBuildButtonReplyMessage_Buttons(t *testing.T) {
	t.Parallel()
	sender := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ButtonReplyMessage{
		Recipient:       sender,
		ButtonID:        "btn-yes",
		DisplayText:     "Yes",
		Kind:            domain.ButtonReplyButtons,
		ContextStanzaID: "3EBB85B4313489CE97",
		ContextSender:   sender,
	}
	out := buildButtonReplyMessage(msg, sender)
	br := out.GetButtonsResponseMessage()
	if br == nil {
		t.Fatal("ButtonsResponseMessage missing")
	}
	if got := br.GetSelectedButtonID(); got != "btn-yes" {
		t.Errorf("buttonID = %q, want %q", got, "btn-yes")
	}
	if got := br.GetSelectedDisplayText(); got != "Yes" {
		t.Errorf("displayText = %q, want %q", got, "Yes")
	}
	if got := br.GetType(); got != waE2E.ButtonsResponseMessage_DISPLAY_TEXT {
		t.Errorf("type = %v, want DISPLAY_TEXT", got)
	}
	if out.GetTemplateButtonReplyMessage() != nil {
		t.Errorf("template field must be nil for Kind=Buttons")
	}
	ctxInfo := br.GetContextInfo()
	if ctxInfo == nil {
		t.Fatal("ContextInfo missing — WhatsApp server returns error 479 without it (#161)")
	}
	if got := ctxInfo.GetStanzaID(); got != "3EBB85B4313489CE97" {
		t.Errorf("ContextInfo.StanzaID = %q, want %q", got, "3EBB85B4313489CE97")
	}
}

// TestBuildButtonReplyMessage_Template — spec 110j FR-003 (#161 amendment):
// Kind=Template maps to *waE2E.TemplateButtonReplyMessage with a populated
// ContextInfo block.
func TestBuildButtonReplyMessage_Template(t *testing.T) {
	t.Parallel()
	sender := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ButtonReplyMessage{
		Recipient:       sender,
		ButtonID:        "tpl-1",
		DisplayText:     "Tap me",
		Kind:            domain.ButtonReplyTemplate,
		ContextStanzaID: "3EBB85B4313489CE97",
		ContextSender:   sender,
	}
	out := buildButtonReplyMessage(msg, sender)
	tb := out.GetTemplateButtonReplyMessage()
	if tb == nil {
		t.Fatal("TemplateButtonReplyMessage missing")
	}
	if got := tb.GetSelectedID(); got != "tpl-1" {
		t.Errorf("selectedID = %q, want %q", got, "tpl-1")
	}
	if got := tb.GetSelectedDisplayText(); got != "Tap me" {
		t.Errorf("displayText = %q, want %q", got, "Tap me")
	}
	if out.GetButtonsResponseMessage() != nil {
		t.Errorf("buttons field must be nil for Kind=Template")
	}
	ctxInfo := tb.GetContextInfo()
	if ctxInfo == nil {
		t.Fatal("ContextInfo missing — WhatsApp server returns error 479 without it (#161)")
	}
	if got := ctxInfo.GetStanzaID(); got != "3EBB85B4313489CE97" {
		t.Errorf("ContextInfo.StanzaID = %q, want %q", got, "3EBB85B4313489CE97")
	}
}

// TestMediaTooLarge32200 — T1-08 / R-01: a file above 16 MiB fails with
// domain.ErrMessageTooLarge, which the socket layer maps to
// -32201 MediaTooLarge (not -32200 — see socket/errcodes.go; the task
// description in tasks-tier1.md §T1-08 mis-quoted the code).
func TestMediaTooLarge32200(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	// 16 MiB + 1 byte, written to a real file so os.Stat sees it.
	tmp := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(domain.MaxMediaBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err = a.Send(context.Background(), domain.MediaMessage{
		Recipient: to, Path: tmp, Mime: "application/octet-stream",
	})
	if err == nil {
		t.Fatal("want ErrMessageTooLarge; got nil")
	}
	if !errors.Is(err, domain.ErrMessageTooLarge) {
		t.Fatalf("want ErrMessageTooLarge; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("oversize media must not reach SendMessage; got %d calls", len(fc.SentMessages))
	}
}
