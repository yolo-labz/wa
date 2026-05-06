package rest

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// TokenStore is the spec 110d extension on top of the env-var
// EnvTokenAuth. Any backend that can map an inbound bearer token to
// (id, scope) and report active/revoked/expired status satisfies it.
type TokenStore interface {
	// Verify returns the granted scope for a valid token, or an
	// error when the token is unknown / revoked / expired.
	Verify(ctx context.Context, rawToken string) (Scope, string, error)
}

// Scope is the rest-package mirror of sqlitetokens.Scope. We keep
// the type local to avoid the rest package importing the secondary
// adapter.
type Scope string

// Scope string constants. Mirror sqlitetokens.Scope values exactly.
// Spec 110d.
const (
	ScopeReadStr  Scope = "read"
	ScopeSendStr  Scope = "send"
	ScopeAdminStr Scope = "admin"
)

// MethodScope returns the numeric MethodScope for s. Unknown values
// map to a sentinel that AllowedScope's "fail closed" branch
// rejects.
func (s Scope) MethodScope() MethodScope {
	switch s {
	case ScopeReadStr:
		return ScopeRead
	case ScopeSendStr:
		return ScopeSend
	case ScopeAdminStr:
		return ScopeAdmin
	}
	return 0 // unknown — every method comparison fails
}

// scopedAuth wraps a TokenStore with the Authenticator interface.
// Verify returns nil when the token is valid AND its scope covers
// the requested method. Spec 110d.
type scopedAuth struct {
	store  TokenStore
	method string // populated per request via WithMethod
}

// NewScopedAuth returns an Authenticator backed by the supplied
// TokenStore. Per-request method binding happens later via the
// scope-aware variant of handleRPC (the request-scoped middleware
// ladder lives in server.go).
func NewScopedAuth(store TokenStore) Authenticator {
	return &scopedAuth{store: store}
}

// Verify implements Authenticator. Reads the bearer token from the
// Authorization header, looks it up via the store, and accepts when
// the scope satisfies the per-method requirement.
func (a *scopedAuth) Verify(r *http.Request) error {
	if a == nil || a.store == nil {
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
	scope, _, err := a.store.Verify(r.Context(), presented)
	if err != nil {
		// Map sentinel errors to ErrUnauthorized — never leak the
		// reason on the wire (revoked vs expired vs unknown). The
		// daemon-side log records the granular cause for operators.
		return errors.Join(ErrUnauthorized, err)
	}
	// Scope check delayed to handleRPC (which knows the method).
	// Stash the scope on the request context for the handler.
	*r = *r.WithContext(context.WithValue(r.Context(), ctxKeyScope{}, scope))
	return nil
}

// ctxKeyScope is the request-context key under which Verify stashes
// the granted scope. Unexported to prevent foreign packages from
// faking auth.
type ctxKeyScope struct{}

// scopeFromContext returns the granted scope set by scopedAuth, or
// ScopeAdminStr if none was set (back-compat with the env-var
// EnvTokenAuth path which grants admin implicitly). When a scoped
// store is wired but the context carries no scope, fail closed —
// that means the env-var path was bypassed AND the store path
// somehow returned nil error without setting the value.
func scopeFromContext(ctx context.Context, hasScopedStore bool) Scope {
	if v := ctx.Value(ctxKeyScope{}); v != nil {
		if s, ok := v.(Scope); ok {
			return s
		}
	}
	if hasScopedStore {
		return Scope("") // unknown — comparison fails
	}
	return ScopeAdminStr
}
