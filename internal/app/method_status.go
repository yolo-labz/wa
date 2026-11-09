package app

import (
	"context"
	"encoding/json"
)

// statusResult is the JSON-RPC result for "status" (FR-027).
type statusResult struct {
	healthResult
	JID string `json:"jid,omitempty"`
}

// groupEntry is one element in the groups result list (FR-028).
//
// Subject is set by group admins — attacker-controllable text. Per FR-005a
// it MUST NOT reach an LLM-facing response unwrapped, so handleGroups leaves
// Subject empty and routes the value through the `<channel source="wa">`
// envelope in Channel instead. CLI table renderers fall back to Channel when
// Subject is empty.
type groupEntry struct {
	JID          string   `json:"jid"`
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
	Channel      string   `json:"channel,omitempty"`
}

// groupsResult is the JSON-RPC result for "groups".
type groupsResult struct {
	Groups []groupEntry `json:"groups"`
}

// handleStatus implements the "status" JSON-RPC method (FR-027).
//
// It returns live connection and storage health, with a masked account identifier.
//
// Status bypasses the safety pipeline (FR-013, FR-017).
func (d *Dispatcher) handleStatus(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	res := statusResult{healthResult: d.healthSnapshot(ctx)}
	res.Schema, res.JID = "", res.account
	return marshalResult(res)
}

// handleGroups implements the "groups" JSON-RPC method (FR-028).
//
// It calls GroupManager.List and marshals each group as
// {jid, subject, participants}. Bypasses the safety pipeline (FR-013, FR-017).
func (d *Dispatcher) handleGroups(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	groups, err := d.groups.List(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]groupEntry, 0, len(groups))
	for _, g := range groups {
		ps := make([]string, len(g.Participants))
		for i, p := range g.Participants {
			ps[i] = p.String()
		}
		e := groupEntry{
			JID:          g.JID.String(),
			Participants: ps,
		}
		// FR-005a: the group subject is attacker-controllable (set by admins);
		// fold it into the channel envelope, leaving the raw field empty.
		e.Channel = ChannelWrapFieldsIf(g.Subject, InboundFields{GroupSubject: g.Subject}, g.JID, g.JID, 0)
		entries = append(entries, e)
	}
	return marshalResult(groupsResult{Groups: entries})
}
