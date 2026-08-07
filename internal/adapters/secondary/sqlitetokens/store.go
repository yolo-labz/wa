package sqlitetokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	// modernc.org/sqlite — pure-Go, CGO-free per repo invariant
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Scope is the coarse permission level granted to a token. Spec 110d.
type Scope string

// Token scopes — see internal/adapters/primary/rest/scope.go for the
// per-method MethodScopes table.
const (
	ScopeRead  Scope = "read"  // status, messages, history, search, etc.
	ScopeSend  Scope = "send"  // read + send/sendMedia/react/markRead/draft.*
	ScopeAdmin Scope = "admin" // send + allow.*/pair/contact.block/group.*/etc.
)

// IsValid reports whether s is one of the three accepted scopes.
func (s Scope) IsValid() bool {
	switch s {
	case ScopeRead, ScopeSend, ScopeAdmin:
		return true
	}
	return false
}

// Token is the wire-friendly view of a token row. Raw is non-empty
// only on Issue() — every other path returns a zero Raw to enforce
// the "show once" contract.
type Token struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Scope      Scope     `json:"scope"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	RevokedAt  time.Time `json:"revokedAt"`
	Raw        string    `json:"raw,omitempty"` // only present on Issue()
}

// Sentinel errors.
var (
	ErrInvalidScope    = errors.New("sqlitetokens: invalid scope")
	ErrTokenNotFound   = errors.New("sqlitetokens: token not found")
	ErrTokenRevoked    = errors.New("sqlitetokens: token revoked")
	ErrTokenExpired    = errors.New("sqlitetokens: token expired")
	ErrTokenWrongScope = errors.New("sqlitetokens: scope insufficient for method")
)

// Store wraps the tokens.db SQLite file. Methods are safe for
// concurrent use.
type Store struct {
	db     *sql.DB
	dbPath string

	mu       sync.Mutex
	pending  map[string]int64 // hex(hash) → unix-time, batched last_used_at writes
	flushTTL time.Duration

	flushCancel context.CancelFunc
	flushDone   chan struct{}
}

// Option tunes a Store before Open returns.
type Option func(*storeOpts)

type storeOpts struct {
	flushTTL time.Duration
}

// WithFlushInterval overrides the background last_used_at flush
// cadence. Default is 60s. Test-only; production callers should
// stick to the default.
func WithFlushInterval(d time.Duration) Option {
	return func(o *storeOpts) {
		if d > 0 {
			o.flushTTL = d
		}
	}
}

// Open ensures the parent directory exists with mode 0700, opens the
// SQLite database with the standard PRAGMAs (WAL, synchronous=NORMAL,
// foreign_keys=ON, busy_timeout=5000), applies the embedded schema,
// and chmods the file to 0600.
func Open(ctx context.Context, dbPath string, opts ...Option) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqlitetokens: dbPath must not be empty")
	}
	parent := filepath.Dir(dbPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitetokens: mkdir %s: %w", parent, err)
	}
	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitetokens: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitetokens: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitetokens: schema: %w", err)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlitetokens: chmod: %w", err)
		}
	}
	cfg := storeOpts{flushTTL: 60 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	flushCtx, flushCancel := context.WithCancel(context.Background())
	s := &Store{
		db:          db,
		dbPath:      dbPath,
		pending:     make(map[string]int64),
		flushTTL:    cfg.flushTTL,
		flushCancel: flushCancel,
		flushDone:   make(chan struct{}),
	}
	// #nosec G118 — flusher lifetime is the Store's, not the call site's.
	// The ctx passed to Open is for schema/init only; the flusher runs
	// until Close cancels its own scoped context.
	go s.runFlusher(flushCtx)
	return s, nil
}

// runFlusher is the background goroutine that flushes buffered
// last_used_at writes every flushTTL. Without this, writes are only
// flushed at Close() time, so a daemon crash loses every
// last_used_at update since process start. Codex review §MAJOR on
// PR 110d flagged this.
func (s *Store) runFlusher(ctx context.Context) {
	defer close(s.flushDone)
	ticker := time.NewTicker(s.flushTTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.flushPending(context.Background())
		}
	}
}

// Close stops the background flusher, flushes any pending writes,
// and closes the underlying SQL handle.
func (s *Store) Close() error {
	if s.flushCancel != nil {
		s.flushCancel()
		<-s.flushDone
	}
	if err := s.flushPending(context.Background()); err != nil {
		// Best-effort flush. Log via the caller's slog if needed.
		_ = err
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Issue mints a fresh 32-byte token, persists its sha256 + metadata,
// and returns the Token with Raw populated. Raw is the only chance
// the operator has to capture the secret. ttl <= 0 means "never
// expires"; otherwise expires_at = now + ttl.
func (s *Store) Issue(ctx context.Context, name string, scope Scope, ttl time.Duration) (Token, error) {
	if !scope.IsValid() {
		return Token{}, ErrInvalidScope
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Token{}, errors.New("sqlitetokens: token name must not be empty")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, fmt.Errorf("sqlitetokens: rand: %w", err)
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))

	now := time.Now().UTC()
	id := ulid.Make().String()
	var expiresAt int64
	if ttl > 0 {
		expiresAt = now.Add(ttl).Unix()
	}

	const q = `INSERT INTO tokens (token_id, hash, name, scope, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, id, sum[:], name, string(scope), now.Unix(), expiresAt); err != nil {
		return Token{}, fmt.Errorf("sqlitetokens: insert: %w", err)
	}

	tok := Token{
		ID:        id,
		Name:      name,
		Scope:     scope,
		CreatedAt: now,
		Raw:       rawHex,
	}
	if expiresAt > 0 {
		tok.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	}
	return tok, nil
}

// Verify looks up the token by sha256(rawToken) and returns its
// metadata. Returns ErrTokenNotFound, ErrTokenRevoked, or
// ErrTokenExpired as appropriate. Constant-time compare on the
// hash defeats timing oracles. Successful verification updates
// last_used_at via a buffered writer that flushes every flushTTL.
func (s *Store) Verify(ctx context.Context, rawToken string) (Token, error) {
	if rawToken == "" {
		return Token{}, ErrTokenNotFound
	}
	sum := sha256.Sum256([]byte(rawToken))
	const q = `SELECT token_id, hash, name, scope, created_at, expires_at, last_used_at, revoked_at
FROM tokens
WHERE hash = ?
LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, sum[:])

	var (
		tok                            Token
		hash                           []byte
		expiresAt, lastUsed, revokedAt sql.NullInt64
		createdAt                      int64
		scope                          string
	)
	if err := row.Scan(&tok.ID, &hash, &tok.Name, &scope, &createdAt, &expiresAt, &lastUsed, &revokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, ErrTokenNotFound
		}
		return Token{}, fmt.Errorf("sqlitetokens: scan: %w", err)
	}
	// Constant-time sanity check (the hash matched in the WHERE
	// clause, but the comparison defends against future schema
	// changes that might let two rows collide on a prefix).
	if subtle.ConstantTimeCompare(hash, sum[:]) != 1 {
		return Token{}, ErrTokenNotFound
	}
	tok = tokenFromRow(tok, scope, createdAt, expiresAt, lastUsed, revokedAt)
	if revokedAt.Valid {
		return tok, ErrTokenRevoked
	}
	now := time.Now().UTC()
	if !tok.ExpiresAt.IsZero() && now.After(tok.ExpiresAt) {
		return tok, ErrTokenExpired
	}

	s.touch(hash, now)
	return tok, nil
}

// tokenFromRow fills the time/scope fields shared by every row scan.
// Verify and List both project SQL NULLs into zero values here.
func tokenFromRow(tok Token, scope string, createdAt int64, expiresAt, lastUsed, revokedAt sql.NullInt64) Token {
	tok.Scope = Scope(scope)
	tok.CreatedAt = time.Unix(createdAt, 0).UTC()
	if expiresAt.Valid && expiresAt.Int64 > 0 {
		tok.ExpiresAt = time.Unix(expiresAt.Int64, 0).UTC()
	}
	if lastUsed.Valid {
		tok.LastUsedAt = time.Unix(lastUsed.Int64, 0).UTC()
	}
	if revokedAt.Valid {
		tok.RevokedAt = time.Unix(revokedAt.Int64, 0).UTC()
	}
	return tok
}

// Revoke marks the token row as revoked. Idempotent — re-revoking
// is a no-op (preserves the original revoked_at). Returns
// ErrTokenNotFound when no row matches.
func (s *Store) Revoke(ctx context.Context, tokenID string) error {
	const q = `UPDATE tokens SET revoked_at = ?
WHERE token_id = ? AND revoked_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, time.Now().UTC().Unix(), tokenID)
	if err != nil {
		return fmt.Errorf("sqlitetokens: revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either the id doesn't exist OR the token was already revoked.
		// Distinguish by re-querying.
		var revoked sql.NullInt64
		if err := s.db.QueryRowContext(
			ctx,
			`SELECT revoked_at FROM tokens WHERE token_id = ?`, tokenID,
		).Scan(&revoked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrTokenNotFound
			}
			return fmt.Errorf("sqlitetokens: revoke check: %w", err)
		}
		// Already revoked → idempotent.
	}
	return nil
}

// List returns every token row, newest-first. Raw is always empty.
func (s *Store) List(ctx context.Context) ([]Token, error) {
	const q = `SELECT token_id, name, scope, created_at, expires_at, last_used_at, revoked_at
FROM tokens
ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlitetokens: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Token, 0)
	for rows.Next() {
		var (
			tok                            Token
			scope                          string
			createdAt                      int64
			expiresAt, lastUsed, revokedAt sql.NullInt64
		)
		if err := rows.Scan(&tok.ID, &tok.Name, &scope, &createdAt, &expiresAt, &lastUsed, &revokedAt); err != nil {
			return nil, fmt.Errorf("sqlitetokens: scan list: %w", err)
		}
		tok = tokenFromRow(tok, scope, createdAt, expiresAt, lastUsed, revokedAt)
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitetokens: rows: %w", err)
	}
	return out, nil
}

// Sweep deletes revoked or expired tokens whose revocation/expiry
// timestamp is older than `olderThan`. Returns the number of rows
// deleted. Pass zero or negative `olderThan` for "delete every
// inactive row immediately".
func (s *Store) Sweep(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Unix()
	if olderThan <= 0 {
		cutoff = time.Now().UTC().Unix() + 1
	}
	const q = `DELETE FROM tokens
WHERE (revoked_at IS NOT NULL AND revoked_at < ?)
   OR (expires_at > 0 AND expires_at < ?)`
	res, err := s.db.ExecContext(ctx, q, cutoff, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sqlitetokens: sweep: %w", err)
	}
	return res.RowsAffected()
}

// touch records a last_used_at update without immediately writing
// to disk. The buffered writer flushes every flushTTL or on Close().
func (s *Store) touch(hash []byte, now time.Time) {
	key := hex.EncodeToString(hash)
	s.mu.Lock()
	s.pending[key] = now.Unix()
	s.mu.Unlock()
}

// FlushPending forces the pending last_used_at writes to disk.
// Called by tests + Close().
func (s *Store) FlushPending(ctx context.Context) error {
	return s.flushPending(ctx)
}

func (s *Store) flushPending(ctx context.Context) error {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return nil
	}
	pending := s.pending
	s.pending = make(map[string]int64)
	s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitetokens: begin flush: %w", err)
	}
	const q = `UPDATE tokens SET last_used_at = ? WHERE hash = ?`
	prepared, err := tx.PrepareContext(ctx, q)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlitetokens: prepare flush: %w", err)
	}
	defer func() { _ = prepared.Close() }()
	for keyHex, ts := range pending {
		hash, err := hex.DecodeString(keyHex)
		if err != nil {
			continue
		}
		if _, err := prepared.ExecContext(ctx, ts, hash); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlitetokens: exec flush: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitetokens: commit flush: %w", err)
	}
	return nil
}
