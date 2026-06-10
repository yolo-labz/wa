package sqlitewebhooks

import (
	"context"
	"fmt"
	"strconv"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// disableAfterDeadStreak is how many consecutive dead deliveries an
// endpoint survives before it is auto-disabled. Protects a long-gone
// receiver from accumulating queue rows forever; `wa webhook add` the
// endpoint again (or replay after fixing it) to resume.
const disableAfterDeadStreak = 5

// AddEndpoint persists a new endpoint row.
func (s *Store) AddEndpoint(ctx context.Context, ep app.WebhookEndpoint) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO webhook_endpoints (id, profile, url, secret, topics, disabled, disable_reason, failure_streak, created_at)
		VALUES (?, ?, ?, ?, ?, 0, '', 0, ?)`,
		ep.ID, ep.Profile, ep.URL, ep.Secret, ep.Topics, ep.CreatedAt)
	if err != nil {
		return fmt.Errorf("sqlitewebhooks: add endpoint: %w", err)
	}
	return nil
}

// ListEndpoints returns every endpoint for the profile, newest first.
func (s *Store) ListEndpoints(ctx context.Context, profile string) ([]app.WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile, url, secret, topics, disabled, disable_reason, failure_streak, created_at
		FROM webhook_endpoints WHERE profile = ? ORDER BY created_at DESC`, profile)
	if err != nil {
		return nil, fmt.Errorf("sqlitewebhooks: list endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []app.WebhookEndpoint
	for rows.Next() {
		var ep app.WebhookEndpoint
		var disabled int
		if err := rows.Scan(&ep.ID, &ep.Profile, &ep.URL, &ep.Secret, &ep.Topics,
			&disabled, &ep.DisableReason, &ep.FailureStreak, &ep.CreatedAt); err != nil {
			return nil, fmt.Errorf("sqlitewebhooks: scan endpoint: %w", err)
		}
		ep.Disabled = disabled != 0
		out = append(out, ep)
	}
	return out, rows.Err()
}

// RemoveEndpoint deletes an endpoint and its deliveries.
func (s *Store) RemoveEndpoint(ctx context.Context, profile, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM webhook_endpoints WHERE profile = ? AND id = ?`, profile, id)
	if err != nil {
		return fmt.Errorf("sqlitewebhooks: remove endpoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrWebhookNotFound
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM webhook_deliveries WHERE profile = ? AND endpoint_id = ?`, profile, id)
	if err != nil {
		return fmt.Errorf("sqlitewebhooks: remove deliveries: %w", err)
	}
	return nil
}

// Enqueue inserts a pending delivery row.
func (s *Store) Enqueue(ctx context.Context, d app.WebhookDelivery) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO webhook_deliveries (id, profile, endpoint_id, topic, payload, state, attempts, next_attempt_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', 0, ?, '', ?, ?)`,
		d.ID, d.Profile, d.EndpointID, d.Topic, d.Payload, d.NextAttemptAt, d.CreatedAt, d.CreatedAt)
	if err != nil {
		return fmt.Errorf("sqlitewebhooks: enqueue: %w", err)
	}
	return nil
}

// Due returns pending deliveries whose next attempt is at or before
// now, oldest first, joined with their endpoint's url+secret. Disabled
// endpoints are skipped (their rows stay pending and age out via
// RemoveEndpoint or re-enable-by-re-add).
func (s *Store) Due(ctx context.Context, profile string, now int64, limit int) ([]app.WebhookDue, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.profile, d.endpoint_id, d.topic, d.payload, d.attempts, e.url, e.secret
		FROM webhook_deliveries d
		JOIN webhook_endpoints e ON e.profile = d.profile AND e.id = d.endpoint_id
		WHERE d.profile = ? AND d.state = 'pending' AND d.next_attempt_at <= ? AND e.disabled = 0
		ORDER BY d.next_attempt_at ASC LIMIT ?`, profile, now, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitewebhooks: due: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []app.WebhookDue
	for rows.Next() {
		var d app.WebhookDue
		if err := rows.Scan(&d.ID, &d.Profile, &d.EndpointID, &d.Topic, &d.Payload, &d.Attempts, &d.URL, &d.Secret); err != nil {
			return nil, fmt.Errorf("sqlitewebhooks: scan due: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkDelivered flips a delivery to delivered and resets the
// endpoint's failure streak.
func (s *Store) MarkDelivered(ctx context.Context, profile, id, endpointID string, at int64) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE webhook_deliveries SET state = 'delivered', updated_at = ?, last_error = ''
		WHERE profile = ? AND id = ?`, at, profile, id); err != nil {
		return fmt.Errorf("sqlitewebhooks: mark delivered: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE webhook_endpoints SET failure_streak = 0 WHERE profile = ? AND id = ?`,
		profile, endpointID); err != nil {
		return fmt.Errorf("sqlitewebhooks: reset streak: %w", err)
	}
	return nil
}

// MarkFailed records a failed attempt. dead=false reschedules at
// nextAt; dead=true tombstones the delivery, bumps the endpoint's
// failure streak and auto-disables the endpoint once the streak
// reaches disableAfterDeadStreak.
func (s *Store) MarkFailed(ctx context.Context, profile, id, endpointID string, attempts int, nextAt int64, lastErr string, dead bool, at int64) error {
	state := "pending"
	if dead {
		state = "dead"
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE webhook_deliveries SET state = ?, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ?
		WHERE profile = ? AND id = ?`,
		state, attempts, nextAt, lastErr, at, profile, id); err != nil {
		return fmt.Errorf("sqlitewebhooks: mark failed: %w", err)
	}
	if !dead {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE webhook_endpoints SET
			failure_streak = failure_streak + 1,
			disabled = CASE WHEN failure_streak + 1 >= ? THEN 1 ELSE disabled END,
			disable_reason = CASE WHEN failure_streak + 1 >= ? THEN 'auto-disabled: ' || ? || ' consecutive dead deliveries' ELSE disable_reason END
		WHERE profile = ? AND id = ?`,
		disableAfterDeadStreak, disableAfterDeadStreak, strconv.Itoa(disableAfterDeadStreak),
		profile, endpointID); err != nil {
		return fmt.Errorf("sqlitewebhooks: bump streak: %w", err)
	}
	return nil
}

// Deliveries lists delivery rows, optionally filtered by state
// (""=all), newest first.
func (s *Store) Deliveries(ctx context.Context, profile, state string, limit int) ([]app.WebhookDelivery, error) {
	q := `SELECT id, profile, endpoint_id, topic, payload, state, attempts, next_attempt_at, last_error, created_at, updated_at
		FROM webhook_deliveries WHERE profile = ?`
	args := []any{profile}
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitewebhooks: deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []app.WebhookDelivery
	for rows.Next() {
		var d app.WebhookDelivery
		if err := rows.Scan(&d.ID, &d.Profile, &d.EndpointID, &d.Topic, &d.Payload,
			&d.State, &d.Attempts, &d.NextAttemptAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("sqlitewebhooks: scan delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Replay re-arms one delivery (any state) for an immediate attempt.
func (s *Store) Replay(ctx context.Context, profile, id string, now int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE webhook_deliveries SET state = 'pending', next_attempt_at = ?, updated_at = ?
		WHERE profile = ? AND id = ?`, now, now, profile, id)
	if err != nil {
		return fmt.Errorf("sqlitewebhooks: replay: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrWebhookNotFound
	}
	return nil
}

// EnabledEndpoints returns non-disabled endpoints for fan-out.
func (s *Store) EnabledEndpoints(ctx context.Context, profile string) ([]app.WebhookEndpoint, error) {
	eps, err := s.ListEndpoints(ctx, profile)
	if err != nil {
		return nil, err
	}
	out := eps[:0]
	for _, ep := range eps {
		if !ep.Disabled {
			out = append(out, ep)
		}
	}
	return out, nil
}
