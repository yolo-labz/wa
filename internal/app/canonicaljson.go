package app

import (
	"crypto/sha256"
)

// CanonicalParamsHash returns the SHA-256 digest of the canonical-JSON
// representation of v. Uses the same canonicalJSON helper that the
// 017 idempotency path uses (sorted keys, reserved fields stripped)
// so the same params value produces the same hash under either
// contract. Callers pass the result into IdempotencyStore.LoadOrStore.
func CanonicalParamsHash(v any) ([32]byte, error) {
	raw, err := canonicalJSON(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
