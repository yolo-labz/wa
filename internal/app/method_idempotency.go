package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// idempotencyEnvelope peels the top-level `idempotencyKey` field off
// any JSON-RPC params payload without touching the rest of the shape.
// An omitted or empty key disables the sidecar per FR-034a.
type idempotencyEnvelope struct {
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// extractIdempotencyKey peeks `idempotencyKey` from raw JSON-RPC params.
// Returns "" when params is nil/empty, unparseable, or the field is absent.
// Never returns an error — a malformed payload is caught later by the
// typed parseParams call inside the actual handler.
func extractIdempotencyKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var env idempotencyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	return env.IdempotencyKey
}

// paramsFingerprint canonicalises raw JSON-RPC params (sorted keys,
// reserved fields stripped) and returns the SHA-256 of the canonical
// bytes. Used as the IdempotencyStore.LoadOrStore paramsHash so the same
// call with a matching key replays its cached bytes and a different body
// under the same key yields domain.ErrIdempotencyCollision → -32101.
func paramsFingerprint(raw json.RawMessage) ([32]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return [32]byte{}, err
	}
	canon, err := canonicalJSON(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canon), nil
}

// idempotentCall is the FR-034a wrapper that every mutating JSON-RPC
// handler runs its body through. Rules:
//
//   - empty key OR nil sidecar  →  invoke run(ctx) verbatim, no cache touch.
//   - non-empty key + sidecar   →  compute paramsHash from raw, delegate to
//     IdempotencyStore.LoadOrStore. A same-key replay with matching hash
//     returns the cached bytes; a mismatched hash yields
//     domain.ErrIdempotencyCollision which the socket dispatcher maps
//     to -32101 idempotency_collision.
//
// The callback's error is preserved verbatim through the LoadOrStore
// boundary — wrapping it in a sentinel would lose the original code the
// caller depends on (ErrInvalidJID, ErrNotAllowlisted, etc.).
func (d *Dispatcher) idempotentCall(
	ctx context.Context,
	method string,
	raw json.RawMessage,
	run func(context.Context) (json.RawMessage, error),
) (json.RawMessage, error) {
	key := extractIdempotencyKey(raw)
	if key == "" || d.idempotency == nil {
		return run(ctx)
	}
	hash, err := paramsFingerprint(raw)
	if err != nil {
		return nil, ErrInvalidParams
	}
	var innerErr error
	cached, err := d.idempotency.LoadOrStore(ctx, method, d.profile, key, hash,
		func() ([]byte, error) {
			out, rerr := run(ctx)
			if id, ok := uncertainID(rerr); ok {
				// The frame may be out: record that under the key, so a
				// retry is told the same thing instead of sending again.
				return json.Marshal(map[string]string{uncertainKey: id})
			}
			if rerr != nil {
				innerErr = rerr
				return nil, rerr
			}
			return []byte(out), nil
		})
	if innerErr != nil {
		return nil, innerErr
	}
	if err != nil {
		return nil, err
	}
	if id, ok := uncertainReplay(cached); ok {
		return nil, domain.NewSendUncertain(domain.MessageID(id), errUncertainRecorded)
	}
	return json.RawMessage(cached), nil
}

// uncertainKey is the one field of the record a send_uncertain outcome leaves
// under its idempotency key; no method result has it.
const uncertainKey = "wa_send_uncertain_id"

var errUncertainRecorded = errors.New("recorded under this idempotency key")

func uncertainID(err error) (string, bool) {
	var u *domain.SendUncertainError
	if errors.As(err, &u) {
		return string(u.MessageID), true
	}
	return "", false
}

func uncertainReplay(b []byte) (string, bool) {
	var m map[string]string
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &m) != nil || len(m) != 1 {
		return "", false
	}
	id, ok := m[uncertainKey]
	return id, ok
}
