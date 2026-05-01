package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// contactResolveParams is the JSON-RPC params for "contact.resolve".
// `jid` is the JID to resolve; the resolver auto-detects whether it
// is a PN or LID and returns the alternate-namespace JID if known.
type contactResolveParams struct {
	JID string `json:"jid"`
}

// contactResolveResult is the on-wire shape of a "contact.resolve"
// response. `Input` is the canonical form of the input JID; `Alt` is
// the resolved alternate-namespace JID, or empty when no mapping is
// known. `Kind` is the input kind ("pn" or "lid") for client-side
// branching.
type contactResolveResult struct {
	Input string `json:"input"`
	Alt   string `json:"alt,omitempty"`
	Kind  string `json:"kind"`
	Known bool   `json:"known"`
}

// handleContactResolve implements "contact.resolve". Spec 106.
//
// Behaviour table:
//
//	input is PN  → calls ResolveLID, returns alt=LID  (kind="pn")
//	input is LID → calls ResolvePN,  returns alt=PN   (kind="lid")
//	input is anything else → ErrNotIdentity (-32015)
//
// A successful resolution that returns the zero JID is reported as
// `{known: false, alt: ""}` — the wire body distinguishes "no mapping
// yet" from "input invalid".
func (d *Dispatcher) handleContactResolve(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.identity == nil {
		return nil, ErrMethodNotFound
	}
	var p contactResolveParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.JID == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, ErrInvalidJID
	}

	var (
		alt  domain.JID
		kind string
	)
	switch {
	case jid.IsUser():
		kind = "pn"
		alt, err = d.identity.ResolveLID(ctx, jid)
	case jid.IsLID():
		kind = "lid"
		alt, err = d.identity.ResolvePN(ctx, jid)
	default:
		return nil, ErrNotIdentity
	}
	if err != nil {
		return nil, fmt.Errorf("contact.resolve: %w", err)
	}

	return marshalResult(contactResolveResult{
		Input: jid.String(),
		Alt:   alt.String(), // empty string when alt.IsZero()
		Kind:  kind,
		Known: !alt.IsZero(),
	})
}
