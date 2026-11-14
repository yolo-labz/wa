package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// callerKey marks a request context with the transport it arrived on.
// Unexported so only WithRemoteCaller and WithLocalCaller can set it.
type callerKey struct{}

// Network transports, as named in the confinement refusal log.
const (
	TransportREST = "rest"
	TransportMCP  = "mcp"

	transportLocal = "local" // the daemon user's own socket
)

// WithRemoteCaller marks ctx as a network caller arriving over transport,
// overriding any earlier local mark.
func WithRemoteCaller(ctx context.Context, transport string) context.Context {
	return context.WithValue(ctx, callerKey{}, transport)
}

// WithLocalCaller marks ctx as the daemon user's own caller: the unix
// socket (0600, same euid), which the stdio MCP bridge also rides. Only
// this mark exempts a daemon-host path from confinePath.
func WithLocalCaller(ctx context.Context) context.Context {
	return context.WithValue(ctx, callerKey{}, transportLocal)
}

// IsRemoteCaller is fail-closed: every context not marked local is remote,
// so a new transport (listener, webhook, admin UI, job worker) is confined
// until it opts in.
func IsRemoteCaller(ctx context.Context) bool { return transportOf(ctx) != transportLocal }

func transportOf(ctx context.Context) string {
	if v, _ := ctx.Value(callerKey{}).(string); v != "" {
		return v
	}
	return "unmarked"
}

// errRemotePath is the single refusal for every rejected remote path. A
// missing file, an escape, and a non-file all answer the same, so the
// error is not an existence oracle for the daemon's filesystem.
var errRemotePath = InvalidParams("path must name a file inside the daemon media directory; upload with POST /media/upload and send by sha256")

// confinePath returns the path the daemon should read, logging one Warn
// line per refusal with the method and transport so canary refusals are
// countable. The path value is never logged: it is caller-supplied and may
// name files on the daemon host.
func (d *Dispatcher) confinePath(ctx context.Context, method, p string) (string, error) {
	resolved, err := d.resolveRemotePath(ctx, p)
	if err != nil {
		d.log.WarnContext(ctx, "remote media path refused", "method", method, "transport", transportOf(ctx))
	}
	return resolved, err
}

// confinePathPtr confines an optional path in place.
func (d *Dispatcher) confinePathPtr(ctx context.Context, method string, p *string) error {
	if p == nil {
		return nil
	}
	var err error
	*p, err = d.confinePath(ctx, method, *p)
	return err
}

// resolveRemotePath returns the path the daemon should read. Local callers get
// p back verbatim. For a remote caller it resolves p to its real path
// (no symlink or ".." escape) and requires a regular file under the
// configured media root; with no root configured every remote path is
// refused (fail closed).
//
// ponytail: check-then-read leaves a symlink-swap window inside the
// root; the root is daemon-written and content-addressed, so a remote
// caller cannot plant links there. Upgrade path is openat2(RESOLVE_BENEATH)
// once the adapter reads from a passed fd instead of a path.
func (d *Dispatcher) resolveRemotePath(ctx context.Context, p string) (string, error) {
	if p == "" || !IsRemoteCaller(ctx) {
		return p, nil
	}
	if d.remotePathRoot == "" || !filepath.IsAbs(p) {
		return "", errRemotePath
	}
	root, err := filepath.EvalSymlinks(d.remotePathRoot)
	if err != nil {
		return "", errRemotePath
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", errRemotePath
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errRemotePath
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.Mode().IsRegular() {
		return "", errRemotePath
	}
	return resolved, nil
}
