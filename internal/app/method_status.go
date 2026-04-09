package app

import (
	"context"
	"encoding/json"
)

// statusResult is the JSON-RPC result for "status" (FR-027).
type statusResult struct {
	Connected bool   `json:"connected"`
	JID       string `json:"jid,omitempty"`
}

// groupEntry is one element in the groups result list (FR-028).
type groupEntry struct {
	JID          string   `json:"jid"`
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
}

// groupsResult is the JSON-RPC result for "groups".
type groupsResult struct {
	Groups []groupEntry `json:"groups"`
}

// handleStatus implements the "status" JSON-RPC method (FR-027).
//
// It returns the current connection state. For now, the connection state
// is approximated from the session store: if a session exists, report
// connected=true and the session's JID. This will be refined in feature
// 006 when the adapter exposes real-time connection state.
//
// Status bypasses the safety pipeline (FR-013, FR-017).
func (d *Dispatcher) handleStatus(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	sess, err := d.session.Load(ctx)
	if err != nil {
		return nil, err
	}

	res := statusResult{}
	if !sess.IsZero() {
		res.Connected = true
		res.JID = sess.JID().String()
	}
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
		entries = append(entries, groupEntry{
			JID:          g.JID.String(),
			Subject:      g.Subject,
			Participants: ps,
		})
	}
	return marshalResult(groupsResult{Groups: entries})
}
