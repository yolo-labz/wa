package sqlitedrafts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// ErrNotFound is returned when no draft matches the lookup criteria.
var ErrNotFound = errors.New("sqlitedrafts: not found")

// Put implements app.DraftStore. Stores a draft; replaces on id conflict
// (primary key is id, so INSERT OR REPLACE is safe — callers validate
// state transitions via domain helpers before calling Put).
func (s *Store) Put(ctx context.Context, d domain.Draft) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("sqlitedrafts: put: %w", err)
	}
	const q = `
INSERT INTO drafts
    (id, profile, kind, payload_json, target_jid, state,
     created_at, expires_at, decided_at, decided_by, reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    profile      = excluded.profile,
    kind         = excluded.kind,
    payload_json = excluded.payload_json,
    target_jid   = excluded.target_jid,
    state        = excluded.state,
    created_at   = excluded.created_at,
    expires_at   = excluded.expires_at,
    decided_at   = excluded.decided_at,
    decided_by   = excluded.decided_by,
    reason       = excluded.reason
`
	_, err := s.db.ExecContext(ctx, q,
		d.ID, d.Profile, int(d.Kind), d.PayloadJSON,
		d.TargetJID.String(), int(d.State),
		d.CreatedAt, d.ExpiresAt, d.DecidedAt, string(d.DecidedBy), d.Reason,
	)
	if err != nil {
		return fmt.Errorf("sqlitedrafts: put: %w", err)
	}
	return nil
}

// Get implements app.DraftStore.
func (s *Store) Get(ctx context.Context, profile, id string) (domain.Draft, error) {
	const q = `
SELECT id, profile, kind, payload_json, target_jid, state,
       created_at, expires_at, decided_at, decided_by, reason
FROM drafts
WHERE profile = ? AND id = ?
`
	row := s.db.QueryRowContext(ctx, q, profile, id)
	d, err := scanDraft(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Draft{}, fmt.Errorf("%w: %s/%s", ErrNotFound, profile, id)
		}
		return domain.Draft{}, err
	}
	return d, nil
}

// List implements app.DraftStore.
func (s *Store) List(ctx context.Context, profile string, state domain.DraftState, limit int) ([]domain.Draft, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlitedrafts: List: invalid limit %d", limit)
	}
	if !state.IsValid() {
		return nil, fmt.Errorf("sqlitedrafts: List: invalid state %d", state)
	}
	const q = `
SELECT id, profile, kind, payload_json, target_jid, state,
       created_at, expires_at, decided_at, decided_by, reason
FROM drafts
WHERE profile = ? AND state = ?
ORDER BY created_at ASC, id ASC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, profile, int(state), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrafts: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]domain.Draft, 0, limit)
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Approve implements app.DraftStore.
func (s *Store) Approve(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider) (domain.Draft, error) {
	return s.transition(ctx, profile, id, func(d domain.Draft) (domain.Draft, error) {
		return d.Approve(at.Unix(), by)
	})
}

// Reject implements app.DraftStore.
func (s *Store) Reject(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider, reason string) (domain.Draft, error) {
	return s.transition(ctx, profile, id, func(d domain.Draft) (domain.Draft, error) {
		return d.Reject(at.Unix(), by, reason)
	})
}

// ExpireDue implements app.DraftStore. Transitions every pending_review draft
// whose expires_at <= at.Unix() to expired in a single transaction.
func (s *Store) ExpireDue(ctx context.Context, profile string, at time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlitedrafts: expire begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const sel = `
SELECT id, profile, kind, payload_json, target_jid, state,
       created_at, expires_at, decided_at, decided_by, reason
FROM drafts
WHERE profile = ? AND state = ? AND expires_at <= ?
`
	rows, err := tx.QueryContext(ctx, sel, profile, int(domain.DraftPendingReview), at.Unix())
	if err != nil {
		return 0, fmt.Errorf("sqlitedrafts: expire scan: %w", err)
	}
	var drafts []domain.Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			_ = rows.Close()
			return 0, err
		}
		drafts = append(drafts, d)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("sqlitedrafts: expire rows close: %w", err)
	}

	const upd = `
UPDATE drafts
SET state = ?, decided_at = ?, decided_by = ?
WHERE profile = ? AND id = ? AND state = ?
`
	n := 0
	for _, d := range drafts {
		next, err := d.ExpireAt(at.Unix())
		if err != nil {
			return 0, fmt.Errorf("sqlitedrafts: expire %s: %w", d.ID, err)
		}
		if next.State == d.State {
			continue
		}
		if _, err := tx.ExecContext(ctx, upd,
			int(next.State), next.DecidedAt, string(next.DecidedBy),
			profile, d.ID, int(domain.DraftPendingReview),
		); err != nil {
			return 0, fmt.Errorf("sqlitedrafts: expire update: %w", err)
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlitedrafts: expire commit: %w", err)
	}
	return n, nil
}

// transition loads, applies fn via the domain state machine, and persists.
// Wraps the read-modify-write in an IMMEDIATE transaction so concurrent
// approve/reject is serialised.
func (s *Store) transition(ctx context.Context, profile, id string, fn func(domain.Draft) (domain.Draft, error)) (domain.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("sqlitedrafts: tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const sel = `
SELECT id, profile, kind, payload_json, target_jid, state,
       created_at, expires_at, decided_at, decided_by, reason
FROM drafts
WHERE profile = ? AND id = ?
`
	row := tx.QueryRowContext(ctx, sel, profile, id)
	cur, err := scanDraft(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Draft{}, fmt.Errorf("%w: %s/%s", ErrNotFound, profile, id)
		}
		return domain.Draft{}, err
	}
	next, err := fn(cur)
	if err != nil {
		return domain.Draft{}, err
	}
	const upd = `
UPDATE drafts
SET state = ?, decided_at = ?, decided_by = ?, reason = ?
WHERE profile = ? AND id = ?
`
	if _, err := tx.ExecContext(ctx, upd,
		int(next.State), next.DecidedAt, string(next.DecidedBy), next.Reason,
		profile, id,
	); err != nil {
		return domain.Draft{}, fmt.Errorf("sqlitedrafts: update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Draft{}, fmt.Errorf("sqlitedrafts: commit: %w", err)
	}
	return next, nil
}

// scanner is the minimal surface shared by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanDraft(sc scanner) (domain.Draft, error) {
	var (
		id, profile, payload, targetJID, decidedBy, reason string
		kindI, stateI                                      int
		created, expires, decided                          int64
	)
	if err := sc.Scan(&id, &profile, &kindI, &payload, &targetJID, &stateI,
		&created, &expires, &decided, &decidedBy, &reason,
	); err != nil {
		return domain.Draft{}, err
	}
	var target domain.JID
	if targetJID != "" {
		j, err := domain.Parse(targetJID)
		if err != nil {
			return domain.Draft{}, fmt.Errorf("sqlitedrafts: parse target_jid %q: %w", targetJID, err)
		}
		target = j
	}
	if kindI < 0 || kindI > math.MaxUint8 {
		return domain.Draft{}, fmt.Errorf("sqlitedrafts: kind %d out of uint8 range", kindI)
	}
	if stateI < 0 || stateI > math.MaxUint8 {
		return domain.Draft{}, fmt.Errorf("sqlitedrafts: state %d out of uint8 range", stateI)
	}
	return domain.Draft{
		ID:          id,
		Profile:     profile,
		Kind:        domain.DraftKind(kindI),
		PayloadJSON: payload,
		TargetJID:   target,
		State:       domain.DraftState(stateI),
		CreatedAt:   created,
		ExpiresAt:   expires,
		DecidedAt:   decided,
		DecidedBy:   domain.DraftDecider(decidedBy),
		Reason:      reason,
	}, nil
}
