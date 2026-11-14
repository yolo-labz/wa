package app

import "context"

type principalKey struct{}

// WithCaller attributes dispatches under ctx to p (FR-116-16). Only
// transports call it; nothing a client sends selects the principal.
func WithCaller(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// CallerFrom reports the principal a transport attributed ctx to.
func CallerFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
