package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
)

// IdempotencyKeyTTL is the lifetime of a recorded idempotency key per
// FR-031. Replays beyond this TTL are treated as fresh requests.
const IdempotencyKeyTTL = 24 * 3600 // seconds

// ErrIdempotencyKey categorises all idempotency-key shape and TTL errors.
var ErrIdempotencyKey = errors.New("domain: invalid idempotency key")

// ErrIdempotencyConflict is returned by the IdempotencyStore use case when
// a key is replayed with a different request fingerprint. The socket layer
// maps this to JSON-RPC error -32108 idempotency_key_conflict (FR-032).
var ErrIdempotencyConflict = errors.New("domain: idempotency key fingerprint mismatch")

var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// IdempotencyKey is a value object binding a caller-supplied opaque string
// to the SHA-256 fingerprint of its canonical-JSON request params.
//
// Invariants per data-model §IdempotencyKey and FR-030..FR-033:
//   - value is 1..64 chars, [A-Za-z0-9_-]
//   - fingerprint = SHA-256 of canonical-JSON params (produced by the
//     caller; the domain trusts the 32 bytes it receives)
//   - TTL 24 h; same value + matching fingerprint ⇒ return cached result;
//     same value + different fingerprint ⇒ ErrIdempotencyConflict.
type IdempotencyKey struct {
	Value       string
	Fingerprint [32]byte
}

// NewIdempotencyKey validates value and stores fingerprint verbatim.
func NewIdempotencyKey(value string, fingerprint [32]byte) (IdempotencyKey, error) {
	if !idempotencyKeyRe.MatchString(value) {
		return IdempotencyKey{}, fmt.Errorf("%w: value %q does not match [A-Za-z0-9_-]{1,64}", ErrIdempotencyKey, value)
	}
	if fingerprint == ([32]byte{}) {
		return IdempotencyKey{}, fmt.Errorf("%w: fingerprint is zero", ErrIdempotencyKey)
	}
	return IdempotencyKey{Value: value, Fingerprint: fingerprint}, nil
}

// Validate reports whether the key satisfies its invariants.
func (k IdempotencyKey) Validate() error {
	if !idempotencyKeyRe.MatchString(k.Value) {
		return fmt.Errorf("%w: value %q does not match [A-Za-z0-9_-]{1,64}", ErrIdempotencyKey, k.Value)
	}
	if k.Fingerprint == ([32]byte{}) {
		return fmt.Errorf("%w: fingerprint is zero", ErrIdempotencyKey)
	}
	return nil
}

// Matches reports whether k has the same fingerprint as other. Used by the
// IdempotencyStore replay-vs-conflict decision.
func (k IdempotencyKey) Matches(other IdempotencyKey) bool {
	return k.Value == other.Value && k.Fingerprint == other.Fingerprint
}

// ComputeFingerprint returns the SHA-256 of canonical-JSON-encoded request
// params. The caller is responsible for producing the canonical form
// (idempotencyKey, requestId, and wall-clock timestamps MUST be stripped
// before hashing per FR-033).
func ComputeFingerprint(canonicalParams []byte) [32]byte {
	return sha256.Sum256(canonicalParams)
}
