package sqliteschedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// ErrNotFound wraps fs.ErrNotExist for use.errors.Is detection.
var ErrNotFound = fmt.Errorf("sqliteschedule: %w", fs.ErrNotExist)

// Put inserts a new schedule. Fails when (profile, id) already exists.
func (s *Store) Put(ctx context.Context, ss domain.ScheduledSend) error {
	if err := validate(ss); err != nil {
		return err
	}
	const q = `
INSERT INTO scheduled_sends
    (id, profile, kind, recipient, body, media_path, fire_at, created_at,
     state, updated_at, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`
	_, err := s.db.ExecContext(ctx, q,
		ss.ID(), ss.Profile(), int(ss.Kind()), ss.Recipient().String(),
		ss.Body(), ss.MediaPath(), ss.FireAt().Unix(), ss.CreatedAt().Unix(),
		int(ss.State()), ss.UpdatedAt().Unix(), ss.LastError(),
	)
	if err != nil {
		return fmt.Errorf("sqliteschedule: put: %w", err)
	}
	return nil
}

// Update replaces state/fire_at/last_error for an existing row. Returns
// ErrNotFound if the row is missing.
func (s *Store) Update(ctx context.Context, ss domain.ScheduledSend) error {
	if err := validate(ss); err != nil {
		return err
	}
	const q = `
UPDATE scheduled_sends
SET    kind       = ?,
       recipient  = ?,
       body       = ?,
       media_path = ?,
       fire_at    = ?,
       state      = ?,
       updated_at = ?,
       last_error = ?
WHERE  profile = ? AND id = ?
`
	res, err := s.db.ExecContext(ctx, q,
		int(ss.Kind()), ss.Recipient().String(),
		ss.Body(), ss.MediaPath(), ss.FireAt().Unix(),
		int(ss.State()), ss.UpdatedAt().Unix(), ss.LastError(),
		ss.Profile(), ss.ID(),
	)
	if err != nil {
		return fmt.Errorf("sqliteschedule: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, ss.Profile(), ss.ID())
	}
	return nil
}

// Get returns the row for (profile, id) or ErrNotFound.
func (s *Store) Get(ctx context.Context, profile, id string) (domain.ScheduledSend, error) {
	const q = `
SELECT id, profile, kind, recipient, body, media_path, fire_at, created_at,
       state, updated_at, last_error
FROM   scheduled_sends
WHERE  profile = ? AND id = ?
`
	row := s.db.QueryRowContext(ctx, q, profile, id)
	ss, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ScheduledSend{}, fmt.Errorf("%w: %s/%s", ErrNotFound, profile, id)
		}
		return domain.ScheduledSend{}, err
	}
	return ss, nil
}

// ListPending returns every non-terminal row for profile ordered by fire_at ASC.
func (s *Store) ListPending(ctx context.Context, profile string) ([]domain.ScheduledSend, error) {
	const q = `
SELECT id, profile, kind, recipient, body, media_path, fire_at, created_at,
       state, updated_at, last_error
FROM   scheduled_sends
WHERE  profile = ? AND state = ?
ORDER  BY fire_at ASC, id ASC
`
	rows, err := s.db.QueryContext(ctx, q, profile, int(domain.SchedulePending))
	if err != nil {
		return nil, fmt.Errorf("sqliteschedule: list_pending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanAll(rows)
}

// List returns rows for (profile, state). Zero state returns all states.
func (s *Store) List(ctx context.Context, profile string, state domain.ScheduledState, limit int) ([]domain.ScheduledSend, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if state == 0 {
		const q = `
SELECT id, profile, kind, recipient, body, media_path, fire_at, created_at,
       state, updated_at, last_error
FROM   scheduled_sends
WHERE  profile = ?
ORDER  BY fire_at DESC, id ASC
LIMIT  ?
`
		rows, err = s.db.QueryContext(ctx, q, profile, limit)
	} else {
		const q = `
SELECT id, profile, kind, recipient, body, media_path, fire_at, created_at,
       state, updated_at, last_error
FROM   scheduled_sends
WHERE  profile = ? AND state = ?
ORDER  BY fire_at DESC, id ASC
LIMIT  ?
`
		rows, err = s.db.QueryContext(ctx, q, profile, int(state), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("sqliteschedule: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanAll(rows)
}

// Delete removes a row. Returns ErrNotFound if missing.
func (s *Store) Delete(ctx context.Context, profile, id string) error {
	const q = `DELETE FROM scheduled_sends WHERE profile = ? AND id = ?`
	res, err := s.db.ExecContext(ctx, q, profile, id)
	if err != nil {
		return fmt.Errorf("sqliteschedule: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, profile, id)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scan(r rowScanner) (domain.ScheduledSend, error) {
	var (
		id, profile, recipient, body, mediaPath, lastError string
		kind, state                                        int
		fireAt, createdAt, updatedAt                       int64
	)
	if err := r.Scan(&id, &profile, &kind, &recipient, &body, &mediaPath, &fireAt, &createdAt, &state, &updatedAt, &lastError); err != nil {
		return domain.ScheduledSend{}, err
	}
	jid, err := domain.Parse(recipient)
	if err != nil {
		return domain.ScheduledSend{}, fmt.Errorf("sqliteschedule: scan jid: %w", err)
	}
	// Reconstruct via the raw hydrator (skips NewScheduledSend invariants
	// because stored rows are already validated and we want to preserve
	// terminal states).
	return domain.HydrateScheduledSend(id, profile, domain.ScheduleKind(kind), jid, body, mediaPath,
		time.Unix(fireAt, 0).UTC(), time.Unix(createdAt, 0).UTC(),
		domain.ScheduledState(state), time.Unix(updatedAt, 0).UTC(), lastError), nil
}

func scanAll(rows *sql.Rows) ([]domain.ScheduledSend, error) {
	out := make([]domain.ScheduledSend, 0, 16)
	for rows.Next() {
		ss, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func validate(ss domain.ScheduledSend) error {
	if ss.ID() == "" || ss.Profile() == "" {
		return fmt.Errorf("sqliteschedule: id/profile required")
	}
	if !ss.State().IsValid() {
		return fmt.Errorf("sqliteschedule: invalid state %d", ss.State())
	}
	return nil
}
