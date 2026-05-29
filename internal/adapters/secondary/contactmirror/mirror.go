// Package contactmirror composes the live WhatsApp contact directory with a
// local searchable mirror (contacts.db). It is the composition-root glue that
// makes `contacts.search` / `contacts.sync` work end-to-end:
//
//   - Lookup/Resolve delegate to the live directory (the whatsmeow adapter),
//     preserving the pre-mirror behaviour for point lookups.
//   - Search/Annotate/ListChanged/Upsert delegate to the local mirror store
//     (sqlitecontacts), which owns the FTS index.
//   - Sync enumerates every configured ContactSource (the whatsmeow contact
//     store, the message-history senders) and Upserts each contact into the
//     mirror, so the FTS index reflects everyone the account has talked to.
//
// Before this adapter existed the dispatcher was wired directly to the
// whatsmeow adapter, which implements ContactDirectory but NOT
// ContactSearcher — so `contacts.search` failed the type assertion and
// returned JSON-RPC -32601, and contacts.db stayed empty because nothing ever
// called Upsert (issues #175 and #181).
package contactmirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Mirror satisfies both app.ContactDirectory and app.ContactSearcher so the
// dispatcher's `d.contacts.(ContactSearcher)` assertion succeeds.
type Mirror struct {
	dir     app.ContactDirectory
	store   app.ContactSearcher
	sources []ContactSource
	log     *slog.Logger
}

var (
	_ app.ContactDirectory = (*Mirror)(nil)
	_ app.ContactSearcher  = (*Mirror)(nil)
)

// ContactSource enumerates the contacts known to one origin (the whatsmeow
// contact store, the local message history, …). Sync upserts every returned
// contact into the mirror; sources are queried independently so one failing
// source never blocks the others.
type ContactSource interface {
	AllContacts(ctx context.Context) ([]domain.Contact, error)
}

// ContactSourceFunc adapts a plain function to a ContactSource, so adapters
// can expose an enumerator method without importing this package.
type ContactSourceFunc func(ctx context.Context) ([]domain.Contact, error)

// AllContacts implements ContactSource.
func (f ContactSourceFunc) AllContacts(ctx context.Context) ([]domain.Contact, error) {
	return f(ctx)
}

// New builds a Mirror. dir backs Lookup/Resolve, store backs the searchable
// mirror, and sources feed Sync. log may be nil.
func New(dir app.ContactDirectory, store app.ContactSearcher, log *slog.Logger, sources ...ContactSource) *Mirror {
	return &Mirror{dir: dir, store: store, sources: sources, log: log}
}

// Lookup implements app.ContactDirectory by delegating to the live directory.
func (m *Mirror) Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error) {
	return m.dir.Lookup(ctx, jid)
}

// Resolve implements app.ContactDirectory by delegating to the live directory.
func (m *Mirror) Resolve(ctx context.Context, phone string) (domain.JID, error) {
	return m.dir.Resolve(ctx, phone)
}

// Search implements app.ContactSearcher against the local mirror.
func (m *Mirror) Search(ctx context.Context, query string, limit int) ([]domain.Contact, error) {
	return m.store.Search(ctx, query, limit)
}

// Annotate implements app.ContactSearcher against the local mirror.
func (m *Mirror) Annotate(ctx context.Context, jid domain.JID, notes string, tags []string) error {
	return m.store.Annotate(ctx, jid, notes, tags)
}

// ListChanged implements app.ContactSearcher against the local mirror.
func (m *Mirror) ListChanged(ctx context.Context, sinceSeq int64, limit int) ([]domain.Contact, error) {
	return m.store.ListChanged(ctx, sinceSeq, limit)
}

// Upsert implements app.ContactSearcher against the local mirror.
func (m *Mirror) Upsert(ctx context.Context, c domain.Contact) error {
	return m.store.Upsert(ctx, c)
}

// Sync pulls every source and upserts the union into the mirror. delta and
// full behave identically: the sources expose no incremental cursor, so both
// do a full idempotent re-upsert (Upsert is ON CONFLICT DO UPDATE). The mode
// is validated and logged so a future incremental source can specialise it.
func (m *Mirror) Sync(ctx context.Context, mode app.SyncMode) error {
	if !mode.IsValid() {
		return fmt.Errorf("contactmirror.Sync: invalid mode %v", mode)
	}

	var (
		upserted int
		errs     []error
	)
	for _, src := range m.sources {
		contacts, err := src.AllContacts(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, c := range contacts {
			if c.JID.IsZero() {
				continue
			}
			if err := m.store.Upsert(ctx, c); err != nil {
				errs = append(errs, err)
				continue
			}
			upserted++
		}
	}

	if m.log != nil {
		m.log.Info("contact sync complete",
			"mode", mode.String(),
			"sources", len(m.sources),
			"upserted", upserted,
			"errors", len(errs),
		)
	}

	// Only hard-fail when nothing landed AND at least one source erred —
	// a partial sync (one source down, another populated rows) is success.
	if upserted == 0 && len(errs) > 0 {
		return fmt.Errorf("contactmirror.Sync: all sources failed: %w", errors.Join(errs...))
	}
	return nil
}
