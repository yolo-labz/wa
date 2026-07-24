package whatsmeow

import (
	"context"
	"errors"
	"fmt"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Send implements app.MessageSender. Contract (ports.go §MessageSender):
//   - MS2/MS3: Validate the domain message BEFORE any I/O.
//   - MS4: honour ctx cancellation.
//   - MS1: return a non-zero domain.MessageID on success.
//   - MS5: safe for concurrent use (the underlying whatsmeow client is).
//   - MS6: MediaMessage with a missing path returns a wrapped os.ErrNotExist.
//
// FR-018: eager domain.ErrDisconnected when the adapter is closed or the
// underlying client reports !IsConnected. The caller (a use case in
// internal/app) decides whether to retry or surface the failure — the
// adapter never queues silently.
func sendWrap(err error) error { return fmt.Errorf("MessageSender.Send: %w", err) }

// Send dispatches an outbound message through the whatsmeow client.
func (a *Adapter) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	if msg == nil {
		return "", sendWrap(errors.New("nil message"))
	}
	if err := msg.Validate(); err != nil {
		return "", sendWrap(err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a.closed.Load() {
		return "", sendWrap(domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return "", sendWrap(domain.ErrDisconnected)
	}

	to := toWhatsmeow(msg.To())
	waMsg, err := a.buildOutboundMessage(ctx, msg)
	if err != nil {
		return "", sendWrap(err)
	}

	// Use clientCtx-derived timeout? No — the port contract says honour
	// the caller's ctx. The whatsmeow client itself is governed by
	// clientCtx for its connection state, but per-call RPCs take the
	// caller's context.
	resp, err := a.client.SendMessage(ctx, to, waMsg)
	if err != nil {
		a.recordAuditDetail(domain.AuditSend, msg.To(), "error", err.Error())
		return "", sendWrap(err)
	}

	a.recordAuditDetail(domain.AuditSend, msg.To(), "ok", resp.ID)

	// Feature 009 — FR-004: persist outbound messages to messages.db.
	if a.history != nil {
		body := ""
		mediaType := ""
		caption := ""
		switch m := msg.(type) {
		case domain.TextMessage:
			// Persist the body that actually went on the wire, including any
			// mention tokens EffectiveBody appended, so history matches what
			// the recipient sees.
			body = m.EffectiveBody()
		case domain.MediaMessage:
			mediaType = m.Mime
			caption = m.Caption
			body = caption
		}
		ownJID := ""
		if dev := a.client.Store(); dev != nil && dev.ID != nil {
			ownJID = dev.ID.String()
		}
		// Spec 107: outbound persistence does not have an AddressingMode
		// or SenderAlt — those are inbound-only metadata from the wire.
		// Pass empty so the v5 columns store NULL.
		if err := a.history.InsertRaw(ctx,
			msg.To().String(), ownJID, resp.ID, resp.Timestamp.Unix(),
			body, mediaType, caption, "", true, nil,
			"", "",
		); err != nil {
			a.recordAuditDetail(domain.AuditPanic, msg.To(), "persist_send", err.Error())
		}
	}

	return domain.MessageID(resp.ID), nil
}

// buildOutboundMessage maps a domain.Message onto a whatsmeow
// *waE2E.Message. The sealed sum type guarantees exhaustive coverage.
//
// MediaMessage routes through buildMediaMessage (feature 018 T1-08, R-01).
// ReactionMessage routes through buildReactionMessage (T1-09, R-02).
// ListReplyMessage / ButtonReplyMessage route through the reply-class
// builders (spec 110j FR-003 — reply-only relaxation of FR-131).
func (a *Adapter) buildOutboundMessage(ctx context.Context, msg domain.Message) (*waE2E.Message, error) {
	switch m := msg.(type) {
	case domain.TextMessage:
		// A plain Conversation cannot carry ContextInfo, so a text message
		// with @mentions MUST use the ExtendedTextMessage form. The
		// no-mention path stays byte-identical to before (plain Conversation).
		if len(m.Mentions) > 0 {
			return buildExtendedTextMessage(m), nil
		}
		return &waE2E.Message{
			Conversation: new(m.Body),
		}, nil
	case domain.MediaMessage:
		return buildMediaMessage(ctx, a.client, a.mediaResolver, m)
	case domain.ReactionMessage:
		return buildReactionMessage(m, a.nowFn), nil
	case domain.ListReplyMessage:
		return buildListResponseMessage(m, msg.To()), nil
	case domain.ButtonReplyMessage:
		return buildButtonReplyMessage(m, msg.To()), nil
	default:
		return nil, fmt.Errorf("unknown domain.Message variant: %T", msg)
	}
}

// buildExtendedTextMessage maps a TextMessage carrying @mentions onto a
// waE2E.ExtendedTextMessage. ContextInfo.MentionedJID lists the mentioned
// identities in canonical `<user>@<server>` form; EffectiveBody() guarantees
// the matching `@<number>` tokens are present in the text so the recipient's
// client renders each mention as tappable + notifying. Only reached when
// len(m.Mentions) > 0 (buildOutboundMessage keeps the plain-Conversation path
// for un-mentioned text).
func buildExtendedTextMessage(m domain.TextMessage) *waE2E.Message {
	mentioned := make([]string, 0, len(m.Mentions))
	for _, j := range m.Mentions {
		mentioned = append(mentioned, j.String())
	}
	body := m.EffectiveBody()
	return &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: new(body),
			ContextInfo: &waE2E.ContextInfo{
				MentionedJID: mentioned,
			},
		},
	}
}

// buildContextInfo constructs the ContextInfo block that quotes the
// original interactive message we are replying to. WhatsApp's wire
// protocol REQUIRES four fields populated together — StanzaID,
// Participant, QuotedMessage, and RemoteJID (#161, #163, #165). A
// response missing any one of them is rejected by the server with
// error 479 bad-stanza because the server cannot distinguish a
// reply-class send (allowed) from an unsolicited interactive send
// (FR-131-forbidden) without the quoted echo. Spec 110j FR-003.
//
// quotedRaw is the marshalled *waE2E.Message bytes of the inbound
// interactive being replied to (loaded from the on-disk raw_proto
// blob by the dispatcher via QuotedMessageStore). When empty the
// builder still emits StanzaID + Participant + RemoteJID so the
// dispatcher's "no-store" fallback surfaces the wire-side rejection
// instead of silently producing a non-functional payload.
//
// chat is the recipient JID of the outbound reply (the same address
// used as the `to` field on Client.SendMessage). Baileys + several
// WhatsApp-Web-MD clients populate RemoteJID on ContextInfo for
// quoted replies; smoke against business IVRs returned error 479
// without it. #165.
func buildContextInfo(stanzaID domain.MessageID, sender domain.JID, quotedRaw []byte, chat domain.JID) *waE2E.ContextInfo {
	stanza := string(stanzaID)
	participant := sender.String()
	remote := chat.String()
	ctx := &waE2E.ContextInfo{
		StanzaID:    &stanza,
		Participant: &participant,
		RemoteJID:   &remote,
	}
	if len(quotedRaw) > 0 {
		quoted := &waE2E.Message{}
		if err := proto.Unmarshal(quotedRaw, quoted); err == nil {
			ctx.QuotedMessage = quoted
		}
		// Unmarshal failure is non-fatal at the build site — the wire
		// will reject with error 479 and the dispatcher's audit log
		// captures the original send.listResponse error. Surfacing the
		// raw decode failure here would conflate "corrupt history row"
		// with "WhatsApp wire rejection" and break the existing FR-003
		// adapter contract (builder is pure, no I/O).
	}
	return ctx
}

// buildListResponseMessage maps a domain.ListReplyMessage onto a
// whatsmeow *waE2E.Message carrying a ListResponseMessage with the
// peer-offered SelectedRowID and a ContextInfo quoting the original
// list message. Spec 110j FR-003.
func buildListResponseMessage(m domain.ListReplyMessage, chat domain.JID) *waE2E.Message {
	listType := waE2E.ListResponseMessage_SINGLE_SELECT
	resp := &waE2E.ListResponseMessage{
		Title:    new(m.Title),
		ListType: &listType,
		SingleSelectReply: &waE2E.ListResponseMessage_SingleSelectReply{
			SelectedRowID: new(m.RowID),
		},
		ContextInfo: buildContextInfo(m.ContextStanzaID, m.ContextSender, m.ContextQuotedRaw, chat),
	}
	return &waE2E.Message{ListResponseMessage: resp}
}

// buildButtonReplyMessage maps a domain.ButtonReplyMessage onto either a
// ButtonsResponseMessage (Kind=Buttons) or a TemplateButtonReplyMessage
// (Kind=Template), each carrying a ContextInfo quoting the original
// buttons/template message. Spec 110j FR-003.
func buildButtonReplyMessage(m domain.ButtonReplyMessage, chat domain.JID) *waE2E.Message {
	ctxInfo := buildContextInfo(m.ContextStanzaID, m.ContextSender, m.ContextQuotedRaw, chat)
	switch m.Kind {
	case domain.ButtonReplyTemplate:
		return &waE2E.Message{
			TemplateButtonReplyMessage: &waE2E.TemplateButtonReplyMessage{
				SelectedID:          new(m.ButtonID),
				SelectedDisplayText: new(m.DisplayText),
				ContextInfo:         ctxInfo,
			},
		}
	default:
		respType := waE2E.ButtonsResponseMessage_DISPLAY_TEXT
		return &waE2E.Message{
			ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
				SelectedButtonID: new(m.ButtonID),
				Type:             &respType,
				Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{
					SelectedDisplayText: m.DisplayText,
				},
				ContextInfo: ctxInfo,
			},
		}
	}
}
