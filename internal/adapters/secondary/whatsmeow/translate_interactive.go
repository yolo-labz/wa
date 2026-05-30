package whatsmeow

import (
	"encoding/json"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// extractInteractive maps the list/button reply fields of a whatsmeow
// *waE2E.Message into a domain.InteractivePayload per FR-130. Returns nil
// when the proto carries no recognised interactive response. The function
// is pure and safe to call with a nil msg.
//
// We intentionally do NOT translate outbound *unsolicited* interactive
// messages (ListMessage, ButtonsMessage, InteractiveMessage, TemplateMessage
// with button definitions) — spec 110j splits FR-131 of spec 017: unsolicited
// menus stay forbidden (anti-spam), reply-class echoes (ListResponseMessage,
// ButtonsResponseMessage, TemplateButtonReplyMessage) flow through
// buildOutboundMessage. Only inbound replies materialise a domain payload here.
func extractInteractive(msg *waE2E.Message) *domain.InteractivePayload {
	if msg == nil {
		return nil
	}
	if lr := msg.GetListResponseMessage(); lr != nil {
		sel := lr.GetSingleSelectReply()
		if sel == nil {
			return nil
		}
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveList,
			Prompt:  lr.GetTitle(),
			Options: []domain.InteractiveOption{{
				ID:    sel.GetSelectedRowID(),
				Label: lr.GetTitle(),
			}},
		}
	}
	if br := msg.GetButtonsResponseMessage(); br != nil {
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveButtons,
			Prompt:  br.GetSelectedDisplayText(),
			Options: []domain.InteractiveOption{{
				ID:    br.GetSelectedButtonID(),
				Label: br.GetSelectedDisplayText(),
			}},
		}
	}
	if tb := msg.GetTemplateButtonReplyMessage(); tb != nil {
		return &domain.InteractivePayload{
			Subtype: domain.InteractiveButtons,
			Prompt:  tb.GetSelectedDisplayText(),
			Options: []domain.InteractiveOption{{
				ID:    tb.GetSelectedID(),
				Label: tb.GetSelectedDisplayText(),
			}},
		}
	}
	// Issue #201: native-flow replies are the reply type Cloud-API IVR
	// menus actually use (a `single_select` list pick or a `quick_reply`
	// button tap rendered via InteractiveMessage.NativeFlowMessage). The
	// echo arrives as an InteractiveResponseMessage whose
	// NativeFlowResponseMessage carries the selection as a JSON string in
	// ParamsJSON. Decoding this is what closes the `interactive_json = NULL`
	// gap for native-flow menus.
	if ir := msg.GetInteractiveResponseMessage(); ir != nil {
		if nf := ir.GetNativeFlowResponseMessage(); nf != nil {
			return extractNativeFlowReply(nf, ir.GetBody().GetText())
		}
	}
	return nil
}

// extractNativeFlowReply maps a native-flow response into a
// domain.InteractivePayload (issue #201). prompt is the body text of the
// surrounding InteractiveResponseMessage (may be empty).
//
// ParamsJSON shape: WhatsApp does NOT fix a schema here — it is the
// arbitrary JSON the menu author attached to the selected option, echoed
// back verbatim. Empirically (Cloud-API list_reply / button_reply and
// captured nfm_reply webhooks) a SINGLE selection carries the chosen
// option's identifier under an `id` key and an optional human label under
// `description`/`title`. Multi-field flow *forms* instead carry arbitrary
// answer keys with no `id` — those are not a single-option pick, so we
// surface no Option for them (the body prompt is still preserved) rather
// than guess which key is "the" selection.
//
// Subtype: native flow renders both list pickers and quick-reply buttons.
// The Name field disambiguates ("single_select"/"list" → list, everything
// else incl. "quick_reply"/"cta_url" → buttons). A single tap is
// button-like, so Buttons is the safe default when Name is unset/unknown.
//
// Defensive contract (issue #201): a nil/empty/malformed ParamsJSON never
// panics — it yields a payload carrying only the prompt with empty Options,
// or nil when there is nothing to surface at all.
func extractNativeFlowReply(nf *waE2E.InteractiveResponseMessage_NativeFlowResponseMessage, prompt string) *domain.InteractivePayload {
	subtype := domain.InteractiveButtons
	switch nf.GetName() {
	case "single_select", "list":
		subtype = domain.InteractiveList
	}

	id, label := parseNativeFlowParams(nf.GetParamsJSON())
	if label == "" {
		// Fall back to the body text as the option label when the params
		// carried no human-readable description/title.
		label = prompt
	}

	// Nothing usable at all (no selection id, no prompt) → no payload,
	// matching the nil-return contract of the other reply branches.
	if id == "" && prompt == "" {
		return nil
	}

	payload := &domain.InteractivePayload{
		Subtype: subtype,
		Prompt:  prompt,
	}
	// Only materialise an Option for a genuine single-option selection
	// (a parsed id). A flow-form answer set has no id and surfaces as a
	// prompt-only payload.
	if id != "" {
		payload.Options = []domain.InteractiveOption{{ID: id, Label: label}}
	}
	return payload
}

// parseNativeFlowParams leniently extracts the selected option id and an
// optional human label from a native-flow ParamsJSON string (issue #201).
// Returns ("", "") for empty or malformed input — the caller treats that
// as "no single-option selection". The candidate key lists mirror the
// field names WhatsApp uses across the list_reply / button_reply / flow
// echo variants; the first non-empty match wins.
func parseNativeFlowParams(raw string) (id, label string) {
	if raw == "" {
		return "", ""
	}
	// ParamsJSON values are heterogeneous (strings, arrays, nested
	// objects for flow forms). Decode into json.RawMessage so a non-string
	// value on one key never aborts extraction of the string keys we want.
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		// Malformed / non-object JSON (e.g. a bare array). Be defensive:
		// no panic, no partial state — just report "no selection".
		return "", ""
	}
	id = firstString(m, "id", "selectedId", "selected_id", "row_id")
	label = firstString(m, "description", "title", "selectedDescription", "name")
	return id, label
}

// firstString returns the first key in keys whose value in m decodes to a
// non-empty JSON string. Non-string values (arrays/objects/numbers) are
// skipped rather than coerced.
func firstString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// interactiveWire is the persisted/wire encoding of a
// domain.InteractivePayload (issue #201). It is duplicated — by design —
// from sqlitehistory's interactiveJSON: the hexagonal boundary forbids the
// whatsmeow adapter from importing the sqlitehistory adapter, so the two
// packages agree on the JSON *shape* (the coupling point) rather than a
// shared Go type. Subtype is the lowercase string ("list"/"buttons") so the
// BLOB is human-readable and stable across domain enum reorders. KEEP IN
// SYNC with sqlitehistory.interactiveJSON / UnmarshalInteractive.
type interactiveWire struct {
	Subtype string                  `json:"subtype"`
	Prompt  string                  `json:"prompt"`
	Options []interactiveWireOption `json:"options"`
}

type interactiveWireOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// marshalInteractiveJSON encodes a domain.InteractivePayload to the
// interactive_json BLOB shape persisted by the history store (issue #201).
// The bytes round-trip through sqlitehistory.UnmarshalInteractive on read.
// Callers only invoke this for a non-nil payload.
func marshalInteractiveJSON(p *domain.InteractivePayload) ([]byte, error) {
	w := interactiveWire{
		Subtype: p.Subtype.String(),
		Prompt:  p.Prompt,
		Options: make([]interactiveWireOption, 0, len(p.Options)),
	}
	for _, o := range p.Options {
		w.Options = append(w.Options, interactiveWireOption{ID: o.ID, Label: o.Label})
	}
	return json.Marshal(w)
}
