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
type contactView struct {
	JID      string `json:"jid"`
	PushName string `json:"pushName,omitempty"`
	Verified bool   `json:"verified,omitempty"`
}

func viewContact(c domain.Contact) contactView {
	return contactView{
		JID:      c.JID.String(),
		PushName: c.PushName,
		Verified: c.Verified,
	}
}

// handleContactsLookup implements "contacts.lookup".
func (d *Dispatcher) handleContactsLookup(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsLookupParams
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
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return nil, ErrMethodNotFound
	}
	hits, err := searcher.Search(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.search: %w", err)
	}
	out := make([]contactView, 0, len(hits))
	for _, c := range hits {
		out = append(out, viewContact(c))
	}
	return marshalResult(struct {
		Contacts []contactView `json:"contacts"`
	}{out})
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
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return nil, ErrMethodNotFound
	}
	hits, err := searcher.ListChanged(ctx, 0, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.list: %w", err)
	}
	out := make([]contactView, 0, len(hits))
	for _, c := range hits {
		out = append(out, viewContact(c))
	}
	return marshalResult(struct {
		Contacts []contactView `json:"contacts"`
	}{out})
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
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return nil, ErrMethodNotFound
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
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return nil, ErrMethodNotFound
	}
	if err := searcher.Sync(ctx, mode); err != nil {
		return nil, fmt.Errorf("contacts.sync: %w", err)
	}
	return marshalResult(struct {
		Mode string `json:"mode"`
	}{mode.String()})
}
