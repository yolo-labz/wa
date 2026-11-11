package whatsmeow

import (
	"encoding/json"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// maxUnwrapDepth bounds unwrapMessage. Real traffic nests at most two
// containers (deviceSent → ephemeral → content); the cap only stops a
// crafted self-similar proto from spinning.
const maxUnwrapDepth = 8

// unwrapMessage peels the container messages WhatsApp puts around real
// content. whatsmeow's live path already unwraps (events.Message.UnwrapRaw),
// but history-sync and on-demand history hand us the raw envelope, so a
// view-once image or an ephemeral business template arrived as a wrapper and
// projected to an empty row. editedMessage is deliberately absent: an edit
// is a protocolMessage the edit path stamps onto the original row, not new
// content for this one.
func unwrapMessage(msg *waE2E.Message) *waE2E.Message {
	for range maxUnwrapDepth {
		inner := wrappedMessage(msg)
		if inner == nil {
			return msg
		}
		msg = inner
	}
	return msg
}

func wrappedMessage(m *waE2E.Message) *waE2E.Message {
	for _, inner := range []*waE2E.Message{
		m.GetDeviceSentMessage().GetMessage(),
		m.GetEphemeralMessage().GetMessage(),
		m.GetViewOnceMessage().GetMessage(),
		m.GetViewOnceMessageV2().GetMessage(),
		m.GetViewOnceMessageV2Extension().GetMessage(),
		m.GetDocumentWithCaptionMessage().GetMessage(),
		m.GetBotInvokeMessage().GetMessage(),
		m.GetLottieStickerMessage().GetMessage(),
		m.GetGroupMentionedMessage().GetMessage(),
	} {
		if inner != nil {
			return inner
		}
	}
	return nil
}

// businessContent renders the WhatsApp Business shapes — template,
// interactive, buttons and list messages — as plain text: the message
// text first, then one "[button] …" or "[list] …" line per choice, so a
// reader sees what the sender is asking without a phone. Recruiting and
// notification bots send almost nothing else; before this the daemon named
// these variants (unknownMessageDetail) and stored body="" for every one.
//
// Rendering is read-only: no choice is ever answered from here.
func businessContent(msg *waE2E.Message) (string, bool) {
	var lines []string
	switch {
	case msg.GetTemplateMessage() != nil:
		lines = templateLines(msg.GetTemplateMessage())
	case msg.GetInteractiveMessage() != nil:
		lines = interactiveLines(msg.GetInteractiveMessage())
	case msg.GetButtonsMessage() != nil:
		lines = buttonsLines(msg.GetButtonsMessage())
	case msg.GetListMessage() != nil:
		lines = listLines(msg.GetListMessage())
	default:
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// appendText appends every non-empty string — the one filter all four
// renderers need, since optional proto strings default to "".
func appendText(lines []string, parts ...string) []string {
	for _, p := range parts {
		if p != "" {
			lines = append(lines, p)
		}
	}
	return lines
}

func buttonLine(label, target string) string {
	if label == "" {
		return ""
	}
	if target != "" {
		return "[button] " + label + " → " + target
	}
	return "[button] " + label
}

func templateLines(t *waE2E.TemplateMessage) []string {
	if im := t.GetInteractiveMessageTemplate(); im != nil {
		return interactiveLines(im)
	}
	if fr := t.GetFourRowTemplate(); fr != nil {
		lines := hsmLines(fr.GetHighlyStructuredMessage())
		lines = append(lines, hsmLines(fr.GetContent())...)
		return append(lines, hsmLines(fr.GetFooter())...)
	}
	h := t.GetHydratedFourRowTemplate()
	if h == nil {
		h = t.GetHydratedTemplate()
	}
	lines := appendText(nil, h.GetHydratedTitleText(), h.GetHydratedContentText(), h.GetHydratedFooterText())
	for _, b := range h.GetHydratedButtons() {
		switch {
		case b.GetQuickReplyButton() != nil:
			lines = appendText(lines, buttonLine(b.GetQuickReplyButton().GetDisplayText(), ""))
		case b.GetUrlButton() != nil:
			lines = appendText(lines, buttonLine(b.GetUrlButton().GetDisplayText(), b.GetUrlButton().GetURL()))
		case b.GetCallButton() != nil:
			phone := b.GetCallButton().GetPhoneNumber()
			if phone != "" {
				phone = "tel:" + phone
			}
			lines = appendText(lines, buttonLine(b.GetCallButton().GetDisplayText(), phone))
		}
	}
	return lines
}

// hsmLines reads a four-row template section. Its text only exists once
// hydrated; the un-hydrated form is a server-side template name plus
// parameters, which has no readable sentence to show.
func hsmLines(h *waE2E.HighlyStructuredMessage) []string {
	if hydrated := h.GetHydratedHsm(); hydrated != nil {
		return templateLines(hydrated)
	}
	return nil
}

func interactiveLines(im *waE2E.InteractiveMessage) []string {
	lines := appendText(nil,
		im.GetHeader().GetTitle(), im.GetHeader().GetSubtitle(),
		im.GetBody().GetText(), im.GetFooter().GetText())
	for _, b := range im.GetNativeFlowMessage().GetButtons() {
		lines = append(lines, nativeFlowLines(b)...)
	}
	for _, card := range im.GetCarouselMessage().GetCards() {
		lines = append(lines, interactiveLines(card)...)
	}
	return lines
}

// nativeFlowParams is the subset of a native-flow button's
// buttonParamsJSON that carries readable text. Cloud-API buttons are
// cta_url / cta_call / cta_copy / quick_reply (display_text + target) and
// single_select (a menu title + sections of rows).
type nativeFlowParams struct {
	DisplayText string `json:"display_text"`
	URL         string `json:"url"`
	PhoneNumber string `json:"phone_number"`
	Title       string `json:"title"`
	Sections    []struct {
		Title string `json:"title"`
		Rows  []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"rows"`
	} `json:"sections"`
}

func nativeFlowLines(b *waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton) []string {
	var p nativeFlowParams
	if err := json.Unmarshal([]byte(b.GetButtonParamsJSON()), &p); err != nil {
		// An unparseable button still exists; show its kind rather than
		// pretend the message had no choices.
		return appendText(nil, buttonLine(b.GetName(), ""))
	}
	target := p.URL
	if target == "" && p.PhoneNumber != "" {
		target = "tel:" + p.PhoneNumber
	}
	lines := appendText(nil, buttonLine(p.DisplayText, target))
	if len(p.Sections) > 0 || p.Title != "" {
		lines = appendText(lines, buttonLine(p.Title, ""))
		for _, s := range p.Sections {
			lines = appendText(lines, s.Title)
			for _, r := range s.Rows {
				lines = appendText(lines, listRowLine(r.Title, r.Description))
			}
		}
	}
	return lines
}

func listRowLine(title, description string) string {
	if title == "" {
		return ""
	}
	if description != "" {
		return "[list] " + title + " — " + description
	}
	return "[list] " + title
}

func buttonsLines(b *waE2E.ButtonsMessage) []string {
	lines := appendText(nil, b.GetText(), b.GetContentText(), b.GetFooterText())
	for _, btn := range b.GetButtons() {
		lines = appendText(lines, buttonLine(btn.GetButtonText().GetDisplayText(), ""))
	}
	return lines
}

func listLines(l *waE2E.ListMessage) []string {
	lines := appendText(nil, l.GetTitle(), l.GetDescription(), l.GetFooterText(), buttonLine(l.GetButtonText(), ""))
	for _, s := range l.GetSections() {
		lines = appendText(lines, s.GetTitle())
		for _, r := range s.GetRows() {
			lines = appendText(lines, listRowLine(r.GetTitle(), r.GetDescription()))
		}
	}
	return lines
}

// ContentFromRaw re-projects a stored raw_proto onto (body, media_type,
// caption) with the same messageContent the persist paths use. It is the
// decode half of sqlitehistory.Store.RehydrateEmptyBodies, which recovers
// rows persisted before a variant was understood (h151: business templates).
func ContentFromRaw(raw []byte) (body, mediaType, caption string) {
	msg := &waE2E.Message{}
	if len(raw) == 0 || proto.Unmarshal(raw, msg) != nil {
		return "", "", ""
	}
	return messageContent(msg)
}
