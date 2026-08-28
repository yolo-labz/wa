package app

import (
	"fmt"
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
//
// "Validated upstream" is true of JIDs, which only exist via domain.Parse.
// It was NOT true of the message ids: whatsmeow copies the stanza `id`
// attribute verbatim off the wire, so the sending device chose every byte
// of TargetMessageID and QuotedMessageID and they reached subscribers as
// plain, trusted-looking fields. plainMessageID below is what makes the
// sentence above hold: an id that is not shaped like an id is dropped
// rather than presented, and its field name is listed in RejectedIDs so
// the drop is visible instead of silent (rule 12).

// SubscriberMessageEvent is the subscriber-facing projection of a
// domain.MessageEvent. Untrusted text lives ONLY inside Channel.
type SubscriberMessageEvent struct {
	ID string `json:"id"`
	// MessageID is the WhatsApp stanza id of this message — the handle
	// `media.download`, `reaction.send` and `thread.get` accept. ID is
	// the daemon's event sequence number and is NOT interchangeable
	// with it: a subscriber holding only ID cannot address the message
	// it was just notified about. Empty when the producer carried no
	// stanza id (synthetic and replayed-before-this-field events).
	MessageID string `json:"messageId,omitempty"`
	TS        int64  `json:"ts"`
	Chat      string `json:"chat"`
	Sender    string `json:"sender"`
	// Kind is the message variant, as reported by domain.Message.Variant():
	// "text", "media" (image), "audio", "video", "document", "sticker",
	// "contact", "location", "reaction", "list_reply", "button_reply" or
	// "unknown". Consumers MUST tolerate an unrecognised kind — the set
	// grows as WhatsApp adds message types.
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
	// RejectedIDs names the id fields whose wire value failed
	// domain.MessageID.IsSafe and was therefore withheld — e.g.
	// ["messageId"]. The offending bytes are never echoed. An entry means
	// "a sender put something id-shaped-but-not in this slot", which is
	// worth alerting on; it is normal for this field to be absent.
	RejectedIDs []string `json:"rejectedIds,omitempty"`
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

// SubscriberStreamDropEvent is the subscriber-facing projection of a
// domain.StreamDropEvent — the daemon telling a subscriber it dropped
// events rather than blocking the wire. Every field is daemon-authored,
// so there is no envelope: Reason is one of our own strings.
//
// The field names deliberately match sqliteevents.SyntheticDrop, the
// other producer of the `stream.drop` kind (FR-063, emitted when a
// resuming cursor has fallen off the durable ring). One wire kind with
// two payload schemas would force every consumer to try both, so this
// projection reuses that one. `dropped_total` is absent rather than
// filled with Gap: it is a cumulative ring counter, and a domain event
// covering one gap has no honest value for it.
type SubscriberStreamDropEvent struct {
	ID string `json:"id"`
	TS int64  `json:"ts"`
	// Gap is how many events this drop covers: To - From + 1.
	Gap  uint64 `json:"gap"`
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
	// Reason is the daemon-side cause, e.g. "buffer_full", "resume_gap".
	Reason string `json:"reason"`
}

// UnroutedEvent is what an event variant this bridge has no projection
// for degrades to. It carries only what the sealed domain.Event
// interface guarantees plus the Go type name, because the alternative —
// marshalling the domain struct as-is — hands subscribers whatever
// attacker-controlled text that variant happens to hold, outside the
// FR-005a envelope. Failing closed keeps the event visible (a consumer
// still sees that something happened, and when) while making the
// missing projection obvious in the payload rather than silent.
type UnroutedEvent struct {
	ID string `json:"id"`
	TS int64  `json:"ts"`
	// GoType names the unprojected variant, e.g. "domain.RevokeEvent".
	GoType string `json:"goType"`
}

// wrapMessageEventForSubscribers folds a domain.MessageEvent into its
// subscriber projection. Called from translateDomainEvent so every
// bridge consumer (Events, SubscribeStream, RegisterWaiter) sees the
// wrapped form — the wrap happens exactly once, app-layer-only.
func wrapMessageEventForSubscribers(e domain.MessageEvent) SubscriberMessageEvent {
	// Chat and kind come from the sealed interface, never from a type
	// switch: To() and Variant() are total over the sum type, so a variant
	// this file does not name still projects with the right chat JID and
	// its own kind instead of an empty chat labelled "text".
	//
	// A nil payload is a producer bug, but dereferencing it would panic
	// EventBridge.Run and silently stop delivery for every subscriber
	// (rule 26), so it projects as "unknown" and stays visible.
	var (
		chat     domain.JID
		fields   InboundFields
		target   domain.MessageID
		rejected []string
	)
	kind := "unknown"
	if e.Message != nil {
		chat = e.Message.To()
		kind = e.Message.Variant()
		fields = untrustedFieldsOf(e.Message)
		target = reactionTargetOf(e.Message)
	}
	fields.PushName = e.PushName

	messageID := plainMessageID("messageId", e.MessageID, &rejected)
	targetID := plainMessageID("targetMessageId", target, &rejected)

	var quotedID string
	if e.Quoted != nil {
		quotedID = plainMessageID("quotedMessageId", e.Quoted.MessageID, &rejected)
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
		MessageID:       messageID,
		TS:              e.TS.Unix(),
		Chat:            chat.String(),
		Sender:          e.From.String(),
		Kind:            kind,
		TargetMessageID: targetID,
		QuotedMessageID: quotedID,
		Interactive:     interactive,
		RejectedIDs:     rejected,
		Channel:         ChannelWrapFields(fields, chat, e.From, e.TS.Unix()),
	}
}

// plainMessageID is the gate every plain id field on this DTO passes
// through. A zero id is simply absent — plenty of messages quote nothing
// and react to nothing. An id that is present but not shaped like one is
// withheld and its field name appended to rejected, because the whole
// value of a plain field is that a consumer may trust it: echoing hostile
// bytes there would hand an LLM subscriber attacker prose in the one place
// the FR-005a envelope does not cover.
func plainMessageID(field string, id domain.MessageID, rejected *[]string) string {
	switch {
	case id.IsZero():
		return ""
	case id.IsSafe():
		return string(id)
	default:
		*rejected = append(*rejected, field)
		return ""
	}
}

// untrustedFieldsOf extracts the sender-authored text of a message
// variant into the envelope fields.
//
// Only text a human on the other end actually wrote belongs here.
// Variants with no such surface (audio, sticker) return zero fields —
// their kind and structural ids already say everything true about
// them. UnknownMessage.Detail is deliberately excluded: it is a
// daemon-generated descriptor, and putting it in `body` would present
// it to an LLM consumer as if a contact had typed it.
func untrustedFieldsOf(m domain.Message) InboundFields {
	var fields InboundFields
	switch v := m.(type) {
	case domain.TextMessage:
		fields.Body = v.Body
	case domain.MediaMessage:
		fields.Caption = v.Caption
	case domain.VideoMessage:
		fields.Caption = v.Caption
	case domain.DocumentMessage:
		fields.Caption = v.Caption
	case domain.ContactCard:
		fields.ContactName = v.DisplayName
	case domain.LocationPin:
		fields.LocationName = v.Name
		fields.LocationAddress = v.Address
	case domain.ReactionMessage:
		// A reaction's emoji is free protocol text; treat it as body.
		fields.Body = v.Emoji
	case domain.ListReplyMessage:
		fields.Body = v.Title
	case domain.ButtonReplyMessage:
		fields.Body = v.DisplayText
	}
	return fields
}

// reactionTargetOf returns the message id a reaction points at, or the
// zero id for every other variant. Split from untrustedFieldsOf because
// the target is a structural identifier, not untrusted text: the two have
// different destinations (a plain DTO field vs. the <channel> envelope)
// and different callers, and pairing them forced the body-selector path to
// discard a return it has no use for. It stays a domain.MessageID rather
// than a string so a caller cannot forget the plainMessageID gate.
func reactionTargetOf(m domain.Message) domain.MessageID {
	if r, ok := m.(domain.ReactionMessage); ok {
		return r.TargetID
	}
	return ""
}

// messageBodySelector is the text a `--body-re` subscription filter runs
// against (FR-060): the sender's own words, unwrapped, in-process only.
//
// A caption counts as body — someone filtering for a word does not care
// whether it arrived under a photo or on its own — so it stands in when
// there is no standalone body. Nothing else does: UnknownMessage.Detail
// is daemon-authored, and matching it would fire a filter on text no
// human ever typed.
func messageBodySelector(e domain.MessageEvent) string {
	if e.Message == nil {
		return ""
	}
	fields := untrustedFieldsOf(e.Message)
	if fields.Body != "" {
		return fields.Body
	}
	return fields.Caption
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
			InboundFields{Body: e.NewBody}, e.Chat, e.Sender, e.TS.Unix(),
		),
	}
}

// wrapStreamDropForSubscribers folds a domain.StreamDropEvent into its
// subscriber projection.
func wrapStreamDropForSubscribers(e domain.StreamDropEvent) SubscriberStreamDropEvent {
	return SubscriberStreamDropEvent{
		ID:     string(e.ID),
		TS:     e.TS.Unix(),
		Gap:    e.DroppedCount,
		From:   e.FromSeq,
		To:     e.ToSeq,
		Reason: e.Reason,
	}
}

// unroutedEventOf degrades any event variant with no projection to the
// structural minimum. It reads id and timestamp off the sealed
// domain.Event interface, so it is total: there is no variant it can
// fail on, and none whose text it can leak.
func unroutedEventOf(evt domain.Event) UnroutedEvent {
	return UnroutedEvent{
		ID:     string(evt.EventID()),
		TS:     evt.Timestamp().Unix(),
		GoType: fmt.Sprintf("%T", evt),
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
