package sqlitecontacts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ErrNotFound is returned when no contact matches the lookup criteria.
var ErrNotFound = errors.New("sqlitecontacts: not found")

// Lookup implements app.ContactDirectory.
func (s *Store) Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error) {
	if jid.IsZero() {
		return domain.Contact{}, fmt.Errorf("sqlitecontacts.Lookup: %w", domain.ErrInvalidJID)
	}
	row := s.db.QueryRowContext(ctx, `SELECT jid, push_name, is_business FROM contacts WHERE jid = ?`, jid.String())
	var jidStr, pushName string
	var isBusiness int
	if err := row.Scan(&jidStr, &pushName, &isBusiness); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Contact{}, fmt.Errorf("%w: %s", ErrNotFound, jid)
		}
		return domain.Contact{}, fmt.Errorf("sqlitecontacts: scan: %w", err)
	}
	return domain.Contact{JID: jid, PushName: pushName, Verified: isBusiness == 1}, nil
}

// Resolve implements app.ContactDirectory.
func (s *Store) Resolve(ctx context.Context, phone string) (domain.JID, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(phone), "+")
	if trimmed == "" {
		return domain.JID{}, fmt.Errorf("sqlitecontacts.Resolve: empty phone")
	}
	row := s.db.QueryRowContext(ctx, `SELECT jid FROM contacts WHERE e164 = ? OR e164 = ?`, phone, trimmed)
	var jidStr string
	if err := row.Scan(&jidStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ParsePhone(phone)
		}
		return domain.JID{}, fmt.Errorf("sqlitecontacts: scan: %w", err)
	}
	return domain.Parse(jidStr)
}

// Search implements app.ContactSearcher. Returns up to limit contacts whose
// display_name / push_name / business_name / notes match the trigram query.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.Contact, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("sqlitecontacts.Search: empty query")
	}
	if limit <= 0 || limit > 50 {
		return nil, fmt.Errorf("sqlitecontacts.Search: limit %d out of (0,50]", limit)
	}
	const q = `
SELECT c.jid, c.push_name, c.is_business
FROM contacts_fts
JOIN contacts c ON c.rowid = contacts_fts.rowid
WHERE contacts_fts MATCH ?
ORDER BY c.updated_seq DESC, c.jid ASC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitecontacts: search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]domain.Contact, 0, limit)
	for rows.Next() {
		var jidStr, pushName string
		var isBusiness int
		if err := rows.Scan(&jidStr, &pushName, &isBusiness); err != nil {
			return nil, fmt.Errorf("sqlitecontacts: scan: %w", err)
		}
		j, err := domain.Parse(jidStr)
		if err != nil {
			continue
		}
		out = append(out, domain.Contact{JID: j, PushName: pushName, Verified: isBusiness == 1})
	}
	return out, rows.Err()
}

// Annotate implements app.ContactSearcher.
func (s *Store) Annotate(ctx context.Context, jid domain.JID, notes string, tags []string) error {
	if jid.IsZero() {
		return fmt.Errorf("%w", domain.ErrInvalidJID)
	}
	seq := s.lastSeq.Add(1)
	now := time.Now().Unix()
	tagStr := strings.Join(dedupe(tags), ",")
	_, err := s.db.ExecContext(ctx,
		`UPDATE contacts SET notes = ?, tags = ?, updated_at = ?, updated_seq = ? WHERE jid = ?`,
		notes, tagStr, now, seq, jid.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlitecontacts: annotate: %w", err)
	}
	return nil
}

// ListChanged implements app.ContactSearcher.
func (s *Store) ListChanged(ctx context.Context, sinceSeq int64, limit int) ([]domain.Contact, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitecontacts.ListChanged: invalid limit")
	}
	const q = `SELECT jid, push_name, is_business FROM contacts WHERE updated_seq > ? ORDER BY updated_seq ASC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitecontacts: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]domain.Contact, 0, limit)
	for rows.Next() {
		var jidStr, pushName string
		var isBusiness int
		if err := rows.Scan(&jidStr, &pushName, &isBusiness); err != nil {
			return nil, fmt.Errorf("sqlitecontacts: scan: %w", err)
		}
		j, err := domain.Parse(jidStr)
		if err != nil {
			continue
		}
		out = append(out, domain.Contact{JID: j, PushName: pushName, Verified: isBusiness == 1})
	}
	return out, rows.Err()
}

// Sync implements app.ContactSearcher. The sqlitecontacts adapter does not
// own remote sync — that lives in the whatsmeow adapter. Sync on the store
// is a no-op that returns nil; the whatsmeow adapter calls Upsert to stream
// the delta into this store.
func (s *Store) Sync(ctx context.Context, mode app.SyncMode) error {
	if !mode.IsValid() {
		return fmt.Errorf("sqlitecontacts.Sync: invalid mode %v", mode)
	}
	return nil
}

// Upsert implements app.ContactSearcher. Inserts or updates on jid.
func (s *Store) Upsert(ctx context.Context, c domain.Contact) error {
	if c.JID.IsZero() {
		return fmt.Errorf("%w", domain.ErrInvalidJID)
	}
	seq := s.lastSeq.Add(1)
	now := time.Now().Unix()
	display := c.DisplayName()
	isBusiness := 0
	if c.Verified {
		isBusiness = 1
	}
	const q = `
INSERT INTO contacts (jid, push_name, display_name, is_business, first_seen_at, updated_at, updated_seq)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(jid) DO UPDATE SET
    push_name    = excluded.push_name,
    display_name = excluded.display_name,
    is_business  = excluded.is_business,
    updated_at   = excluded.updated_at,
    updated_seq  = excluded.updated_seq
`
	_, err := s.db.ExecContext(ctx, q, c.JID.String(), c.PushName, display, isBusiness, now, now, seq)
	if err != nil {
		return fmt.Errorf("sqlitecontacts: upsert: %w", err)
	}
	return nil
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
