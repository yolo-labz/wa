package app

import (
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Subscriber-facing event projections (SEC-02, FR-005a).
//
// Push subscribers (REST SSE today; socket notifications and MCP once
// their payloads are wired) receive events that may carry attacker-
// controllable text: message bodies, captions, push names, quote
// previews, interactive prompts/labels. Marshaling the raw domain
// structs across that boundary violates Constitution §V.29 / FR-005a —
// the inbound prompt-injection firewall must wrap every untrusted
// string in the `<channel source="wa">` envelope exactly once, at the
// app layer.
//
// These DTOs are that boundary: every attacker-controllable string is
// folded into the Channel envelope (via ChannelWrapFields) and the raw
// fields are simply absent from the type, so no subscriber can read
// unwrapped text even by accident. Structural identifiers (JIDs,
// message IDs, option IDs, kinds) stay as plain fields — consumers
// need them for correlation and they are validated/parsed upstream.

// SubscriberMessageEvent is the subscriber-facing projection of a
// domain.MessageEvent. Untrusted text lives ONLY inside Channel.
type SubscriberMessageEvent struct {
	ID     string `json:"id"`
	TS     int64  `json:"ts"`
	Chat   string `json:"chat"`
	Sender string `json:"sender"`
	// Kind is the message variant: "text", "media" or "reaction".
	Kind string `json:"kind"`
	// TargetMessageID is set for reactions: the message reacted to.
	TargetMessageID string `json:"targetMessageId,omitempty"`
	// QuotedMessageID is set when the sender quoted a prior message.
	// The quoted preview text itself is inside Channel (quote_preview).
	QuotedMessageID string `json:"quotedMessageId,omitempty"`
	// Interactive carries the structural part of a list/button reply.
	// Prompt and option labels are inside Channel (poll_question /
	// poll_option fields).
	Interactive *SubscriberInteractive `json:"interactive,omitempty"`
	// Channel is the FR-005a `<channel source="wa">` envelope holding
	// every attacker-controllable string of this event.
	Channel string `json:"channel"`
}

// SubscriberInteractive is the structural projection of a
// domain.InteractivePayload: subtype + option IDs. IDs are needed by
// bot consumers to match the selected option; the human-readable labels
// are untrusted and live in the Channel envelope.
type SubscriberInteractive struct {
	Subtype   string   `json:"subtype"`
	OptionIDs []string `json:"optionIds,omitempty"`
}

// SubscriberEditEvent is the subscriber-facing projection of a
// domain.EditEvent. The new body is untrusted and lives in Channel.
type SubscriberEditEvent struct {
	ID                string `json:"id"`
	TS                int64  `json:"ts"`
	Chat              string `json:"chat"`
	Sender            string `json:"sender"`
	OriginalMessageID string `json:"originalMessageId"`
	EditedAt          int64  `json:"editedAt"`
	Channel           string `json:"channel"`
}

// wrapMessageEventForSubscribers folds a domain.MessageEvent into its
// subscriber projection. Called from translateDomainEvent so every
// bridge consumer (Events, SubscribeStream, RegisterWaiter) sees the
// wrapped form — the wrap happens exactly once, app-layer-only.
func wrapMessageEventForSubscribers(e domain.MessageEvent) SubscriberMessageEvent {
	fields := InboundFields{PushName: e.PushName}
	var chat domain.JID
	kind := "text"
	var targetID string

	switch m := e.Message.(type) {
	case domain.TextMessage:
		chat = m.Recipient
		fields.Body = m.Body
	case domain.MediaMessage:
		chat = m.Recipient
		kind = "media"
		fields.Caption = m.Caption
	case domain.ReactionMessage:
		chat = m.Recipient
		kind = "reaction"
		targetID = string(m.TargetID)
		// A reaction's emoji is free protocol text; treat it as body.
		fields.Body = m.Emoji
	}

	var quotedID string
	if e.Quoted != nil {
		quotedID = string(e.Quoted.MessageID)
		fields.QuotePreview = e.Quoted.BodyPreview
	}

	var interactive *SubscriberInteractive
	if e.Interactive != nil {
		ids := make([]string, 0, len(e.Interactive.Options))
		labels := make([]string, 0, len(e.Interactive.Options))
		for _, opt := range e.Interactive.Options {
			ids = append(ids, opt.ID)
			labels = append(labels, opt.Label)
		}
		interactive = &SubscriberInteractive{
			Subtype:   e.Interactive.Subtype.String(),
			OptionIDs: ids,
		}
		fields.PollQuestion = e.Interactive.Prompt
		fields.PollOptions = labels
	}

	return SubscriberMessageEvent{
		ID:              string(e.ID),
		TS:              e.TS.Unix(),
		Chat:            chat.String(),
		Sender:          e.From.String(),
		Kind:            kind,
		TargetMessageID: targetID,
		QuotedMessageID: quotedID,
		Interactive:     interactive,
		Channel:         ChannelWrapFields(fields, chat, e.From, e.TS.Unix()),
	}
}

// wrapEditEventForSubscribers folds a domain.EditEvent into its
// subscriber projection; NewBody is untrusted and goes into Channel.
func wrapEditEventForSubscribers(e domain.EditEvent) SubscriberEditEvent {
	return SubscriberEditEvent{
		ID:                string(e.ID),
		TS:                e.TS.Unix(),
		Chat:              e.Chat.String(),
		Sender:            e.Sender.String(),
		OriginalMessageID: string(e.OriginalID),
		EditedAt:          editedAtOrZero(e.EditedAt),
		Channel: ChannelWrapFields(
			InboundFields{Body: e.NewBody}, e.Chat, e.Sender, e.TS.Unix()),
	}
}

// editedAtOrZero guards the zero time so subscribers see 0, not a
// negative epoch, when whatsmeow omitted the edit timestamp.
func editedAtOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
