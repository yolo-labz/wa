package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// contactsLookupParams is the JSON-RPC params for "contacts.lookup".
type contactsLookupParams struct {
	JID string `json:"jid"`
}

// contactView is the serialised shape of a contact on the wire.
//
// PushName is the contact's self-set display name — attacker-controllable
// text. Per FR-005a it MUST NOT reach an LLM-facing response unwrapped, so
// viewContact leaves PushName empty and routes the value through the
// `<channel source="wa">` envelope in Channel instead. CLI table renderers
// fall back to Channel when PushName is empty.
type contactView struct {
	JID      string `json:"jid"`
	PushName string `json:"pushName,omitempty"`
	Verified bool   `json:"verified,omitempty"`
	Channel  string `json:"channel,omitempty"`
}

func viewContact(c domain.Contact) contactView {
	v := contactView{
		JID:      c.JID.String(),
		Verified: c.Verified,
	}
	// FR-005a: the contact's self-set push name is attacker-controllable;
	// fold it into the channel envelope, leaving the raw field empty. Contacts
	// carry no message ts, so chat+sender are the contact's own JID, ts 0.
	v.Channel = ChannelWrapFieldsIf(c.PushName, InboundFields{ContactName: c.PushName}, c.JID, c.JID, 0)
	return v
}

// contactSearcher returns the ContactSearcher extension of the wired contact
// directory, or ErrMethodNotFound when the adapter does not implement it. It
// folds the type-assertion gate shared by contacts.search/list/annotate/sync.
func (d *Dispatcher) contactSearcher() (ContactSearcher, error) {
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return nil, ErrMethodNotFound
	}
	return searcher, nil
}

// marshalContacts renders a slice of domain contacts into the wire
// `{"contacts": [...]}` envelope, applying the FR-005a viewContact projection
// to each. Shared by contacts.search and contacts.list.
func marshalContacts(hits []domain.Contact) (json.RawMessage, error) {
	out := make([]contactView, 0, len(hits))
	for _, c := range hits {
		out = append(out, viewContact(c))
	}
	return marshalResult(struct {
		Contacts []contactView `json:"contacts"`
	}{out})
}

// parseJIDParam unmarshals raw into dst, then validates and parses the
// single JID string the caller points at. It folds the parseParams →
// required-non-empty → domain.Parse → ErrInvalidJID boilerplate shared by the
// single-JID-param handlers (contacts.lookup/annotate, groups.get).
func parseJIDParam(raw json.RawMessage, dst any, jid *string) (domain.JID, error) {
	if err := parseParams(raw, dst); err != nil {
		return domain.JID{}, err
	}
	if *jid == "" {
		return domain.JID{}, ErrInvalidParams
	}
	parsed, err := domain.Parse(*jid)
	if err != nil {
		return domain.JID{}, ErrInvalidJID
	}
	return parsed, nil
}

// handleContactsLookup implements "contacts.lookup".
func (d *Dispatcher) handleContactsLookup(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsLookupParams
	jid, err := parseJIDParam(raw, &p, &p.JID)
	if err != nil {
		return nil, err
	}
	c, err := d.contacts.Lookup(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("contacts.lookup: %w", err)
	}
	return marshalResult(struct {
		Contact contactView `json:"contact"`
	}{viewContact(c)})
}

// contactsSearchParams is the JSON-RPC params for "contacts.search".
type contactsSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// handleContactsSearch implements "contacts.search".
func (d *Dispatcher) handleContactsSearch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsSearchParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Query == "" {
		return nil, ErrInvalidParams
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Limit > 50 {
		p.Limit = 50
	}
	searcher, err := d.contactSearcher()
	if err != nil {
		return nil, err
	}
	hits, err := searcher.Search(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.search: %w", err)
	}
	return marshalContacts(hits)
}

// contactsListParams is the JSON-RPC params for "contacts.list".
type contactsListParams struct {
	Limit int `json:"limit"`
}

// handleContactsList implements "contacts.list": enumerate the local
// contact directory with no query. Issue #173 / #180 item 3. Reuses the
// ContactSearcher.ListChanged(sinceSeq) surface — every mirrored contact
// carries updated_seq > 0, so sinceSeq=0 returns the whole directory.
func (d *Dispatcher) handleContactsList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsListParams
	if len(raw) > 0 {
		if err := parseParams(raw, &p); err != nil {
			return nil, err
		}
	}
	if p.Limit <= 0 {
		p.Limit = 100
	}
	if p.Limit > 500 {
		p.Limit = 500
	}
	searcher, err := d.contactSearcher()
	if err != nil {
		return nil, err
	}
	hits, err := searcher.ListChanged(ctx, 0, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.list: %w", err)
	}
	return marshalContacts(hits)
}

// contactsAnnotateParams is the JSON-RPC params for "contacts.annotate".
type contactsAnnotateParams struct {
	JID   string   `json:"jid"`
	Notes string   `json:"notes,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

// handleContactsAnnotate implements "contacts.annotate".
func (d *Dispatcher) handleContactsAnnotate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsAnnotateParams
	jid, err := parseJIDParam(raw, &p, &p.JID)
	if err != nil {
		return nil, err
	}
	searcher, err := d.contactSearcher()
	if err != nil {
		return nil, err
	}
	if err := searcher.Annotate(ctx, jid, p.Notes, p.Tags); err != nil {
		return nil, fmt.Errorf("contacts.annotate: %w", err)
	}
	return marshalResult(struct{}{})
}

// contactsSyncParams is the JSON-RPC params for "contacts.sync".
type contactsSyncParams struct {
	Mode string `json:"mode,omitempty"`
}

// handleContactsSync implements "contacts.sync".
func (d *Dispatcher) handleContactsSync(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	p := contactsSyncParams{Mode: "delta"}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}
	var mode SyncMode
	switch p.Mode {
	case "", "delta":
		mode = SyncDelta
	case "full":
		mode = SyncFull
	default:
		return nil, ErrInvalidParams
	}
	searcher, err := d.contactSearcher()
	if err != nil {
		return nil, err
	}
	if err := searcher.Sync(ctx, mode); err != nil {
		return nil, fmt.Errorf("contacts.sync: %w", err)
	}
	return marshalResult(struct {
		Mode string `json:"mode"`
	}{mode.String()})
}
