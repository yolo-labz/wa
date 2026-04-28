package sqlitehistory

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// IdempotencyTTL is the fixed 24-hour rentention window for sidecar rows
// (FR-034a). Rows older than now-TTL are evicted by the sweeper.
const IdempotencyTTL = 24 * time.Hour

// IdempotencySidecar is the sqlite-backed app.IdempotencyStore
// implementation mandated by T1-05. Distinct from the feature-017
// message_idempotency table: this one is the FR-034a
// (method, profile, key) sidecar with an opaque params_hash BLOB.
//
// Safe for concurrent use by multiple goroutines; relies on sqlite's
// serialised writer + WAL readers.
type IdempotencySidecar struct {
	db    *sql.DB
	clock func() time.Time
}

// NewIdempotencySidecar constructs a sidecar bound to db. Callers MUST
// ensure migrateIfNeeded has reached v4 before instantiating. Clock may
// be nil; in that case time.Now is used.
func NewIdempotencySidecar(db *sql.DB, clock func() time.Time) *IdempotencySidecar {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &IdempotencySidecar{db: db, clock: clock}
}

// IdempotencySidecar returns a sidecar bound to this Store's database
// handle. The composition root uses this to wire the FR-034a replay
// cache into the dispatcher without reaching into Store's private fields.
func (s *Store) IdempotencySidecar() *IdempotencySidecar {
	return NewIdempotencySidecar(s.db, nil)
}

// LoadOrStore implements the FR-034a shape. See app.IdempotencyStore
// docstring for the full contract.
func (s *IdempotencySidecar) LoadOrStore(
	ctx context.Context,
	method string,
	profile string,
	key string,
	paramsHash [32]byte,
	execute func() ([]byte, error),
) ([]byte, error) {
	if key == "" {
		return execute()
	}
	// Fast path: cache hit.
	const sel = `SELECT params_hash, response_json FROM idempotency_keys
	             WHERE method = ? AND profile = ? AND key = ? AND expires_at > ?`
	now := s.clock()
	nowSec := now.Unix()
	var stored []byte
	var resp []byte
	err := s.db.QueryRowContext(ctx, sel, method, profile, key, nowSec).Scan(&stored, &resp)
	switch {
	case err == nil:
		if !bytes.Equal(stored, paramsHash[:]) {
			return nil, fmt.Errorf("%w: method=%s key=%s", domain.ErrIdempotencyCollision, method, key)
		}
		out := make([]byte, len(resp))
		copy(out, resp)
		return out, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("sqlitehistory: idempotency lookup: %w", err)
	}

	// Miss: execute then record. Not wrapped in a tx because the execute
	// callback typically performs the side-effecting whatsmeow call which
	// is not db-transactional; we simply insert best-effort after success.
	bytesOut, execErr := execute()
	if execErr != nil {
		return nil, execErr
	}
	const ins = `INSERT OR REPLACE INTO idempotency_keys
	             (method, profile, key, params_hash, response_json, created_at, expires_at)
	             VALUES (?, ?, ?, ?, ?, ?, ?)`
	expires := now.Add(IdempotencyTTL).Unix()
	if _, err := s.db.ExecContext(ctx, ins, method, profile, key, paramsHash[:], bytesOut, nowSec, expires); err != nil {
		return nil, fmt.Errorf("sqlitehistory: idempotency record: %w", err)
	}
	return bytesOut, nil
}

// Sweep deletes expired rows. Safe to call concurrently with LoadOrStore.
// Returns the number of rows evicted.
func (s *IdempotencySidecar) Sweep(ctx context.Context, before time.Time) (int, error) {
	const del = `DELETE FROM idempotency_keys WHERE expires_at <= ?`
	res, err := s.db.ExecContext(ctx, del, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: idempotency sweep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlitehistory: idempotency sweep: rows affected: %w", err)
	}
	return int(n), nil
}

// Check / Record stubs preserve the 017 Check/Record/Sweep surface against
// the sidecar table for callers that already went through
// app.IdempotencyExec. They delegate through a key-only lookup, reusing
// the v4 table. Kept minimal; T1-20 cuts every real caller over to
// LoadOrStore.
func (s *IdempotencySidecar) Check(ctx context.Context, profile string, key domain.IdempotencyKey) ([]byte, bool, error) {
	if err := key.Validate(); err != nil {
		return nil, false, err
	}
	const q = `SELECT params_hash, response_json FROM idempotency_keys
	           WHERE method = '' AND profile = ? AND key = ? AND expires_at > ?`
	var stored, resp []byte
	err := s.db.QueryRowContext(ctx, q, profile, key.Value, s.clock().Unix()).Scan(&stored, &resp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("sqlitehistory: legacy idempotency check: %w", err)
	}
	if !bytes.Equal(stored, key.Fingerprint[:]) {
		return nil, false, fmt.Errorf("%w: key=%s", domain.ErrIdempotencyConflict, key.Value)
	}
	out := make([]byte, len(resp))
	copy(out, resp)
	return out, true, nil
}

// Record is the 017-shape write path. Method column is empty so it does
// not collide with FR-034a rows.
func (s *IdempotencySidecar) Record(ctx context.Context, profile string, key domain.IdempotencyKey, result []byte, expiresAt time.Time) error {
	if err := key.Validate(); err != nil {
		return err
	}
	const ins = `INSERT OR REPLACE INTO idempotency_keys
	             (method, profile, key, params_hash, response_json, created_at, expires_at)
	             VALUES ('', ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, ins, profile, key.Value, key.Fingerprint[:], result, s.clock().Unix(), expiresAt.Unix()); err != nil {
		return fmt.Errorf("sqlitehistory: legacy idempotency record: %w", err)
	}
	return nil
}
