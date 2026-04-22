package app

import (
	"fmt"
	"strings"

	"github.com/yolo-labz/wa/internal/domain"
)

// InboundField is the canonical name of an attacker-controllable string
// field that traverses the `<channel source="wa">` trust boundary. The
// FR-005a corpus enforces that every one of the 12 declared fields round-
// trips through escaping. Do NOT rename a constant; the name is part of
// the wire contract consumed by Claude Code skills.
type InboundField string

// The 12 FR-005a inbound fields. Any new attacker-controllable surface
// MUST be added here AND to InboundFieldsAll below so the corpus fuzzer
// exercises it.
const (
	FieldBody             InboundField = "body"
	FieldCaption          InboundField = "caption"
	FieldPushName         InboundField = "push_name"
	FieldGroupSubject     InboundField = "group_subject"
	FieldGroupDescription InboundField = "group_description"
	FieldPollQuestion     InboundField = "poll_question"
	FieldPollOption       InboundField = "poll_option"
	FieldContactName      InboundField = "contact_name"
	FieldContactOrg       InboundField = "contact_organization"
	FieldLocationName     InboundField = "location_name"
	FieldLocationAddress  InboundField = "location_address"
	FieldQuotePreview     InboundField = "quote_preview"
)

// InboundFieldsAll enumerates every FR-005a-wrapped field in stable
// order. The T1-19 corpus asserts len(InboundFieldsAll) == 12.
var InboundFieldsAll = []InboundField{
	FieldBody,
	FieldCaption,
	FieldPushName,
	FieldGroupSubject,
	FieldGroupDescription,
	FieldPollQuestion,
	FieldPollOption,
	FieldContactName,
	FieldContactOrg,
	FieldLocationName,
	FieldLocationAddress,
	FieldQuotePreview,
}

// InboundFields holds the attacker-controllable string surface of a
// single inbound event. Each non-empty field is wrapped in its own
// `<field name="...">…</field>` child inside the `<channel>` envelope.
// Empty fields are omitted: an LLM consumer should not see a `<field
// name="group_description"></field>` when the group has no description.
//
// PollOptions is a slice — each option emits its own `<field
// name="poll_option" index="N">…</field>` child.
type InboundFields struct {
	Body             string
	Caption          string
	PushName         string
	GroupSubject     string
	GroupDescription string
	PollQuestion     string
	PollOptions      []string
	ContactName      string
	ContactOrg       string
	LocationName     string
	LocationAddress  string
	QuotePreview     string
}

// ChannelWrapFields emits the multi-field FR-005a envelope. Use this at
// the app-layer boundary — every primary adapter (socket, future REST,
// MCP, channel) MUST route inbound text through the app layer so the
// wrap happens exactly once. Wrapping inside a primary adapter is a
// contract violation (FR-005a "app-layer-only").
//
// Wire shape:
//
//	<channel source="wa" chat="..." sender="..." ts="...">
//	  <field name="body">…</field>
//	  <field name="poll_option" index="0">…</field>
//	  <field name="poll_option" index="1">…</field>
//	  …
//	</channel>
//
// Escape contract:
//   - HTML-escape `&`, `<`, `>` on every field value and on every
//     attribute (chat, sender, field name).
//   - Replace C0 control bytes (0x00–0x1F except \t \n \r) with U+FFFD.
//   - Leave U+202E and zero-width characters untouched.
func ChannelWrapFields(f InboundFields, chat, sender domain.JID, ts int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<channel source="wa" chat=%q sender=%q ts="%d">`,
		chat.String(), sender.String(), ts)

	emit := func(name InboundField, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, `<field name=%q>%s</field>`,
			string(name), escapeChannelBody(value))
	}
	emitIdx := func(name InboundField, idx int, value string) {
		fmt.Fprintf(&b, `<field name=%q index="%d">%s</field>`,
			string(name), idx, escapeChannelBody(value))
	}

	emit(FieldBody, f.Body)
	emit(FieldCaption, f.Caption)
	emit(FieldPushName, f.PushName)
	emit(FieldGroupSubject, f.GroupSubject)
	emit(FieldGroupDescription, f.GroupDescription)
	emit(FieldPollQuestion, f.PollQuestion)
	for i, opt := range f.PollOptions {
		emitIdx(FieldPollOption, i, opt)
	}
	emit(FieldContactName, f.ContactName)
	emit(FieldContactOrg, f.ContactOrg)
	emit(FieldLocationName, f.LocationName)
	emit(FieldLocationAddress, f.LocationAddress)
	emit(FieldQuotePreview, f.QuotePreview)

	b.WriteString(`</channel>`)
	return b.String()
}
