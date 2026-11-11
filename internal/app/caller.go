package app

import "context"

type callerKey struct{}

// WithCaller attributes every dispatch under ctx to p (FR-116-16). Only
// transports call it: REST and /mcp with TokenPrincipal or EnvPrincipal,
// the profile's owner-only socket with its SocketPrincipal. Nothing a
// client sends selects the principal.
func WithCaller(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, callerKey{}, p)
}

// CallerFrom reports the principal a transport attributed ctx to.
func CallerFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(callerKey{}).(Principal)
	return p, ok
}
