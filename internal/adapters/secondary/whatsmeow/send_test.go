package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// TestSend_TextMessageMention — PR #292: a TextMessage carrying Mentions is
// sent as an ExtendedTextMessage whose ContextInfo.MentionedJID lists the
// mentioned identities and whose Text has the matching `@<number>` token
// appended (WhatsApp renders a tappable mention only where the token is
// present). This is the daemon half of `wa send --mention`.
func TestSend_TextMessageMention(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.MENTION1"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	group := domain.MustJID("120363000000000000@g.us")
	mention := domain.MustJID("5581999999999@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.TextMessage{
		Recipient: group,
		Body:      "bom dia",
		Mentions:  []domain.JID{mention},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.MENTION1" {
		t.Errorf("want wamid.MENTION1; got %q", id)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	sent := fc.SentMessages[0].Msg
	if sent.GetConversation() != "" {
		t.Errorf("mention send must not use plain Conversation; got %q", sent.GetConversation())
	}
	ext := sent.GetExtendedTextMessage()
	if ext == nil {
		t.Fatalf("expected ExtendedTextMessage, got %+v", sent)
	}
	// EffectiveBody appended the missing token.
	if got := ext.GetText(); got != "bom dia @5581999999999" {
		t.Errorf("Text = %q, want token appended", got)
	}
	ctxInfo := ext.GetContextInfo()
	if ctxInfo == nil {
		t.Fatal("ContextInfo missing — mention would not notify without it")
	}
	got := ctxInfo.GetMentionedJID()
	if len(got) != 1 || got[0] != mention.String() {
		t.Errorf("MentionedJID = %v, want [%s]", got, mention.String())
	}
}

// TestSend_TextMessageNoMentionStaysConversation — PR #292 back-compat: a
// TextMessage without mentions still goes out as a plain Conversation
// (byte-identical to pre-#292), never an ExtendedTextMessage.
func TestSend_TextMessageNoMentionStaysConversation(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PLAIN1"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	if _, err := a.Send(context.Background(), domain.TextMessage{Recipient: to, Body: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := fc.SentMessages[0].Msg
	if got := sent.GetConversation(); got != "hi" {
		t.Errorf("Conversation = %q, want hi", got)
	}
	if sent.GetExtendedTextMessage() != nil {
		t.Error("no-mention text must not use ExtendedTextMessage")
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
// TestSend_MediaMessageInlineBytesUploadsAndSends — spec 197: a MediaMessage
// sourced from inline Bytes (no daemon file) routes through Upload carrying
// exactly those bytes, builds the typed proto, and SendMessage fires once.
func TestSend_MediaMessageInlineBytesUploadsAndSends(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.BYTES1"), Timestamp: fixedNowFn()}

	want := []byte("\xff\xd8\xff\xe0inline-jpeg-bytes")
	var gotPlaintext []byte
	fc.UploadFn = func(_ context.Context, plaintext []byte, _ waClient.MediaType) (waClient.UploadResponse, error) {
		gotPlaintext = append([]byte(nil), plaintext...)
		sum := sha256.Sum256(plaintext)
		return waClient.UploadResponse{
			URL: "https://fake/upload", DirectPath: "/fake/upload",
			MediaKey: make([]byte, 32), FileEncSHA256: sum[:], FileSHA256: sum[:],
			FileLength: uint64(len(plaintext)),
		}, nil
	}

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Bytes:     want,
		Mime:      "image/jpeg",
		Caption:   "from-bytes",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.BYTES1" {
		t.Errorf("want wamid.BYTES1; got %q", id)
	}
	if !bytes.Equal(gotPlaintext, want) {
		t.Errorf("uploaded plaintext mismatch:\n got %q\nwant %q", gotPlaintext, want)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	img := fc.SentMessages[0].Msg.GetImageMessage()
	if img == nil {
		t.Fatalf("expected ImageMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := img.GetCaption(); got != "from-bytes" {
		t.Errorf("caption = %q, want from-bytes", got)
	}
}

// TestSend_MediaMessageInlineBytesTooLarge — spec 197: an inline payload over
// MaxMediaBytes is rejected with ErrMessageTooLarge and never reaches the wire.
func TestSend_MediaMessageInlineBytesTooLarge(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Bytes:     make([]byte, domain.MaxMediaBytes+1),
		Mime:      "application/octet-stream",
	})
	if !errors.Is(err, domain.ErrMessageTooLarge) {
		t.Fatalf("want ErrMessageTooLarge; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("oversize inline bytes must not reach SendMessage; got %d calls", len(fc.SentMessages))
	}
}

// fakeMediaResolver is a mediaResolver double for the SHA256-source send
// branch (spec 198). Objects are content-addressed in a temp dir so the
// adapter can read the bytes off disk exactly as it would against the real
// MediaAdapter.
type fakeMediaResolver struct {
	t    *testing.T
	objs map[[32]byte]domain.MediaObject
}

func newFakeMediaResolver(t *testing.T) *fakeMediaResolver {
	t.Helper()
	return &fakeMediaResolver{t: t, objs: make(map[[32]byte]domain.MediaObject)}
}

// put writes payload to disk under a sha-keyed path and registers the object.
func (f *fakeMediaResolver) put(payload []byte, mimeType, ext string) [32]byte {
	f.t.Helper()
	sum := sha256.Sum256(payload)
	path := filepath.Join(f.t.TempDir(), hex.EncodeToString(sum[:])+"."+ext)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		f.t.Fatalf("fakeMediaResolver put: %v", err)
	}
	f.objs[sum] = domain.MediaObject{
		Ref:          domain.MediaRef{SHA256: sum, Mime: mimeType, Size: int64(len(payload)), Ext: ext},
		Path:         path,
		MimeDetected: mimeType,
	}
	return sum
}

func (f *fakeMediaResolver) Resolve(_ context.Context, sha [32]byte) (domain.MediaObject, error) {
	obj, ok := f.objs[sha]
	if !ok {
		return domain.MediaObject{}, os.ErrNotExist
	}
	return obj, nil
}

// TestSend_MediaMessageSHA256NotWired — spec 197/198: when no media store is
// wired into the adapter, the sha256 source (validated by the domain) must
// fail loudly (ErrMediaUnsupported) rather than silently dropping the send.
func TestSend_MediaMessageSHA256NotWired(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		SHA256:    "deadbeef",
		Mime:      "image/png",
	})
	if !errors.Is(err, domain.ErrMediaUnsupported) {
		t.Fatalf("want ErrMediaUnsupported; got %v", err)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("unwired sha256 must not reach SendMessage; got %d calls", len(fc.SentMessages))
	}
}

// TestSend_MediaMessageSHA256FromStore — spec 198: with a media store wired
// via SetMediaResolver, the sha256 source loads the stored bytes off disk,
// uploads exactly those bytes, builds the typed proto, and SendMessage fires
// once. This is the daemon half of the `--remote sendMedia --sha256` flow.
func TestSend_MediaMessageSHA256FromStore(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.SHA1"), Timestamp: fixedNowFn()}

	want := []byte("\xff\xd8\xff\xe0stored-jpeg-bytes")
	var gotPlaintext []byte
	fc.UploadFn = func(_ context.Context, plaintext []byte, _ waClient.MediaType) (waClient.UploadResponse, error) {
		gotPlaintext = append([]byte(nil), plaintext...)
		sum := sha256.Sum256(plaintext)
		return waClient.UploadResponse{
			URL: "https://fake/upload", DirectPath: "/fake/upload",
			MediaKey: make([]byte, 32), FileEncSHA256: sum[:], FileSHA256: sum[:],
			FileLength: uint64(len(plaintext)),
		}, nil
	}

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	store := newFakeMediaResolver(t)
	sum := store.put(want, "image/jpeg", "jpg")
	a.SetMediaResolver(store)

	to := domain.MustJID("15551234567@s.whatsapp.net")
	id, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		SHA256:    hex.EncodeToString(sum[:]),
		Mime:      "image/jpeg",
		Caption:   "from-sha",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "wamid.SHA1" {
		t.Errorf("want wamid.SHA1; got %q", id)
	}
	if !bytes.Equal(gotPlaintext, want) {
		t.Errorf("uploaded plaintext mismatch:\n got %q\nwant %q", gotPlaintext, want)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	img := fc.SentMessages[0].Msg.GetImageMessage()
	if img == nil {
		t.Fatalf("expected ImageMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := img.GetCaption(); got != "from-sha" {
		t.Errorf("caption = %q, want from-sha", got)
	}
}

// sendDefaultUploadFn installs the standard echo UploadFn on fc and returns
// nothing; media-MIME tests below only assert on the composed proto.
func sendDefaultUploadFn(fc *fakeWhatsmeowClient) {
	fc.UploadFn = func(_ context.Context, plaintext []byte, _ waClient.MediaType) (waClient.UploadResponse, error) {
		sum := sha256.Sum256(plaintext)
		return waClient.UploadResponse{
			URL: "https://fake/upload", DirectPath: "/fake/upload",
			MediaKey: make([]byte, 32), FileEncSHA256: sum[:], FileSHA256: sum[:],
			FileLength: uint64(len(plaintext)),
		}, nil
	}
}

// TestSend_MediaMessageSHA256PDFDefaultMime_NoDotBin — regression for the
// user-reported ".bin" bug (11/06): `wa sendMedia --path x.pdf` against a
// remote daemon uploads the bytes and sends by sha256, so the daemon never
// sees a path. The app layer substitutes the generic octet-stream sentinel
// when the caller sent no mime, and pre-fix the adapter let the sentinel +
// an empty FileName reach the wire — the recipient's WhatsApp rendered the
// PDF as an unopenable ".bin" attachment. The adapter must resolve the real
// MIME (filename extension first) and carry the display filename.
func TestSend_MediaMessageSHA256PDFDefaultMime_NoDotBin(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PDF1"), Timestamp: fixedNowFn()}
	sendDefaultUploadFn(fc)

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF")
	store := newFakeMediaResolver(t)
	sum := store.put(pdf, "application/pdf", "pdf")
	a.SetMediaResolver(store)

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		SHA256:    hex.EncodeToString(sum[:]),
		Mime:      "application/octet-stream", // app-layer sentinel: caller sent no mime
		Filename:  "hemograma.pdf",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fc.SentMessages) != 1 {
		t.Fatalf("want 1 SendMessage; got %d", len(fc.SentMessages))
	}
	doc := fc.SentMessages[0].Msg.GetDocumentMessage()
	if doc == nil {
		t.Fatalf("expected DocumentMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := doc.GetMimetype(); got != "application/pdf" {
		t.Errorf("Mimetype = %q, want application/pdf (octet-stream sentinel must not reach the wire)", got)
	}
	if got := doc.GetFileName(); got != "hemograma.pdf" {
		t.Errorf("FileName = %q, want hemograma.pdf (empty FileName renders as .bin)", got)
	}
	if got := doc.GetTitle(); got != "hemograma.pdf" {
		t.Errorf("Title = %q, want hemograma.pdf", got)
	}
}

// TestSend_MediaMessageSHA256StoreDetectedMime_GeneratedFilename — when the
// caller supplies neither mime nor filename, the sha256 branch falls back to
// the store's sniffed MimeDetected and generates "file.<ext>" so FileName is
// never empty on the wire.
func TestSend_MediaMessageSHA256StoreDetectedMime_GeneratedFilename(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PDF2"), Timestamp: fixedNowFn()}
	sendDefaultUploadFn(fc)

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	pdf := []byte("%PDF-1.7\nminimal")
	store := newFakeMediaResolver(t)
	sum := store.put(pdf, "application/pdf", "pdf")
	a.SetMediaResolver(store)

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		SHA256:    hex.EncodeToString(sum[:]),
		Mime:      "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := fc.SentMessages[0].Msg.GetDocumentMessage()
	if doc == nil {
		t.Fatalf("expected DocumentMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := doc.GetMimetype(); got != "application/pdf" {
		t.Errorf("Mimetype = %q, want application/pdf (store MimeDetected)", got)
	}
	if got := doc.GetFileName(); got != "file.pdf" {
		t.Errorf("FileName = %q, want generated file.pdf", got)
	}
}

// TestSend_MediaMessageBytesPNGDefaultMime_SniffPromotesImage — an inline
// bytes payload with the octet-stream sentinel is content-sniffed; a PNG must
// go out as an ImageMessage with image/png, not as a generic document.
func TestSend_MediaMessageBytesPNGDefaultMime_SniffPromotesImage(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PNG1"), Timestamp: fixedNowFn()}
	sendDefaultUploadFn(fc)

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Bytes:     png,
		Mime:      "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	img := fc.SentMessages[0].Msg.GetImageMessage()
	if img == nil {
		t.Fatalf("expected ImageMessage (sniffed image/png), got %+v", fc.SentMessages[0].Msg)
	}
	if got := img.GetMimetype(); got != "image/png" {
		t.Errorf("Mimetype = %q, want image/png", got)
	}
}

// TestSend_MediaMessagePathPDFDefaultMime — the daemon-local path source also
// resolves the sentinel: extension wins, FileName stays the path basename.
func TestSend_MediaMessagePathPDFDefaultMime(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PDF3"), Timestamp: fixedNowFn()}
	sendDefaultUploadFn(fc)

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	tmp := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(tmp, []byte("%PDF-1.4\nminimal"), 0o600); err != nil {
		t.Fatal(err)
	}

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Path:      tmp,
		Mime:      "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := fc.SentMessages[0].Msg.GetDocumentMessage()
	if doc == nil {
		t.Fatalf("expected DocumentMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := doc.GetMimetype(); got != "application/pdf" {
		t.Errorf("Mimetype = %q, want application/pdf (path extension)", got)
	}
	if got := doc.GetFileName(); got != "report.pdf" {
		t.Errorf("FileName = %q, want report.pdf", got)
	}
}

// TestSend_MediaMessageExplicitMimePreserved — an explicit non-generic mime
// is the operator's override: detection must not second-guess it even when
// the payload sniffs as something else.
func TestSend_MediaMessageExplicitMimePreserved(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.CSV1"), Timestamp: fixedNowFn()}
	sendDefaultUploadFn(fc)

	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	to := domain.MustJID("15551234567@s.whatsapp.net")
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		Bytes:     []byte("%PDF-1.4 actually-not-csv"),
		Mime:      "text/csv",
		Filename:  "data.csv",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := fc.SentMessages[0].Msg.GetDocumentMessage()
	if doc == nil {
		t.Fatalf("expected DocumentMessage, got %+v", fc.SentMessages[0].Msg)
	}
	if got := doc.GetMimetype(); got != "text/csv" {
		t.Errorf("Mimetype = %q, want explicit text/csv preserved", got)
	}
	if got := doc.GetFileName(); got != "data.csv" {
		t.Errorf("FileName = %q, want data.csv", got)
	}
}

// TestSend_MediaMessageSHA256NotInStore — spec 198: a sha that the wired store
// does not hold surfaces the resolve error (never reaches the wire).
func TestSend_MediaMessageSHA256NotInStore(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.SetMediaResolver(newFakeMediaResolver(t)) // empty store

	to := domain.MustJID("15551234567@s.whatsapp.net")
	missing := sha256.Sum256([]byte("never-stored"))
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: to,
		SHA256:    hex.EncodeToString(missing[:]),
		Mime:      "image/png",
	})
	if err == nil {
		t.Fatal("want resolve error for missing sha; got nil")
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("missing sha must not reach SendMessage; got %d calls", len(fc.SentMessages))
	}
}

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

// TestSend_MediaMessagePTTVoiceNote — the two fields that separate a voice
// note from an attached audio file. PTT is what makes the recipient's client
// render a playable waveform bubble; Seconds is the duration it shows before
// downloading. Both have to survive composeMediaProto onto the wire message,
// because nothing downstream can reconstruct them.
func TestSend_MediaMessagePTTVoiceNote(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.PTT"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	tmp := filepath.Join(t.TempDir(), "note.ogg")
	if err := os.WriteFile(tmp, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: domain.MustJID("15551234567@s.whatsapp.net"),
		Path:      tmp,
		Mime:      "audio/ogg; codecs=opus",
		PTT:       true,
		Seconds:   7,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	aud := fc.SentMessages[0].Msg.GetAudioMessage()
	if aud == nil {
		t.Fatal("want AudioMessage")
	}
	if !aud.GetPTT() {
		t.Error("PTT false — the recipient gets a file row, not a voice note")
	}
	if got := aud.GetSeconds(); got != 7 {
		t.Errorf("Seconds = %d, want 7", got)
	}
	// The parametrised MIME is what makes a client draw the waveform, so it
	// must reach the wire unstripped (resolveOutboundMime passes an explicit
	// caller mime through untouched).
	if got := aud.GetMimetype(); got != "audio/ogg; codecs=opus" {
		t.Errorf("Mimetype = %q, want the caller's parametrised type", got)
	}
}

// TestSend_MediaMessageAudioWithoutPTTOmitsBothFields pins the other half of
// the same decision: an ordinary audio attachment must go on the wire exactly
// as it did before the voice-note flags existed. A PTT that is present-and-
// false, or a zero Seconds, is a different serialisation from an absent field.
func TestSend_MediaMessageAudioWithoutPTTOmitsBothFields(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.SendResp = waClient.SendResponse{ID: waTypes.MessageID("wamid.A"), Timestamp: fixedNowFn()}
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	tmp := filepath.Join(t.TempDir(), "clip.ogg")
	if err := os.WriteFile(tmp, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: domain.MustJID("15551234567@s.whatsapp.net"),
		Path:      tmp, Mime: "audio/ogg",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	aud := fc.SentMessages[0].Msg.GetAudioMessage()
	if aud == nil {
		t.Fatal("want AudioMessage")
	}
	if aud.PTT != nil {
		t.Errorf("PTT set to %v on a plain audio send; want the field absent", *aud.PTT)
	}
	if aud.Seconds != nil {
		t.Errorf("Seconds set to %d on a plain audio send; want the field absent", *aud.Seconds)
	}
}

// TestSend_MediaMessagePTTOnNonAudioRefused — Send validates before it
// uploads, so a voice-note flag on an image costs neither an upload nor a
// rate-limit token. Without the domain gate this reached composeMediaProto's
// image branch, which has nowhere to put PTT, and the flag vanished silently.
func TestSend_MediaMessagePTTOnNonAudioRefused(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	tmp := filepath.Join(t.TempDir(), "pic.jpg")
	if err := os.WriteFile(tmp, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.Send(context.Background(), domain.MediaMessage{
		Recipient: domain.MustJID("15551234567@s.whatsapp.net"),
		Path:      tmp, Mime: "image/jpeg", PTT: true,
	})
	if err == nil {
		t.Fatal("want an error for ptt on an image send")
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("refused send still reached the wire: %d message(s)", len(fc.SentMessages))
	}
}
