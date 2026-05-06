package rest

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrUnauthorized is returned when the bearer token presented does not
// match the daemon's configured token. Surfaces as 401 on the wire.
// Per Constitution rule 12 (no silent fallbacks), this is a typed
// sentinel callers can errors.Is — they must NOT default-return-200
// on auth failure.
var ErrUnauthorized = errors.New("rest: unauthorized")

// Authenticator verifies an inbound HTTP request. Returns nil when
// the request carries a valid token. 110a implements only the env-var
// single-token form; 110c will swap in a sqlite-backed multi-token
// store while keeping this interface stable.
type Authenticator interface {
	Verify(r *http.Request) error
}

// EnvTokenAuth verifies a constant-time-compared bearer token whose
// expected value is read from the WAD_REST_TOKEN env var at startup.
// The token is hashed once at construction so the request hot path
// only compares fixed-length sha256 digests, not raw secrets.
type EnvTokenAuth struct {
	expectedHash [sha256.Size]byte
	enabled      bool
}

// MinTokenBytes is the minimum length of an inbound bearer token.
// 32 bytes (= 128 bits, or 64 hex chars / 43 base64 chars) is the
// floor; below that the operator is using a trivially-guessable
// secret. The composition root validates this at startup so a
// short token surfaces immediately rather than enabling weak auth.
// Codex review on PR 110a flagged the absence of this gate.
const MinTokenBytes = 32

// NewEnvTokenAuth returns an EnvTokenAuth keyed by the supplied raw
// token. An empty token disables the authenticator (Verify always
// returns ErrUnauthorized) so misconfigured deploys fail closed
// rather than open. The caller is expected to read WAD_REST_TOKEN
// from os.Getenv and pass the value here.
//
// This constructor accepts ANY non-empty token. Length validation
// happens at the composition-root level (cmd/wad/rest_http.go) so a
// programming error in tests or future callers does not silently
// pass a 1-byte token through. See ValidateTokenStrength.
func NewEnvTokenAuth(rawToken string) *EnvTokenAuth {
	a := &EnvTokenAuth{}
	if rawToken == "" {
		// Disabled: every Verify call returns ErrUnauthorized.
		// The composition root MUST refuse to start the REST listener
		// when WAD_REST_TOKEN is unset; this fallback exists so a
		// programming error (constructing the authenticator with an
		// empty string) does not silently allow all traffic.
		return a
	}
	a.expectedHash = sha256.Sum256([]byte(rawToken))
	a.enabled = true
	return a
}

// ValidateTokenStrength returns an error when the supplied token is
// shorter than MinTokenBytes. The composition root calls this on
// WAD_REST_TOKEN at startup and refuses to start the REST adapter
// on weak tokens — short tokens are a configuration mistake, not a
// runtime auth failure (which a malicious actor could use to probe
// for token format).
func ValidateTokenStrength(rawToken string) error {
	if len(rawToken) < MinTokenBytes {
		return fmt.Errorf("rest: WAD_REST_TOKEN must be at least %d bytes (got %d); generate with `openssl rand -hex 32`", MinTokenBytes, len(rawToken))
	}
	return nil
}

// Verify implements Authenticator. Constant-time compare on the
// sha256 digest defeats timing oracles. Token is read from the
// `Authorization: Bearer <token>` header — no support for
// query-string or cookie auth (both are footguns for CLI/agent
// surfaces and are explicitly rejected per spec 110 §"Alternatives
// rejected E").
func (a *EnvTokenAuth) Verify(r *http.Request) error {
	if a == nil || !a.enabled {
		return ErrUnauthorized
	}
	hdr := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(hdr, prefix) {
		return ErrUnauthorized
	}
	presented := strings.TrimPrefix(hdr, prefix)
	if presented == "" {
		return ErrUnauthorized
	}
	got := sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(got[:], a.expectedHash[:]) != 1 {
		return ErrUnauthorized
	}
	return nil
}
