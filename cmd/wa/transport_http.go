package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

// noRedirectClient is the shared HTTP client for every --remote call.
// CheckRedirect returns ErrUseLastResponse so a proxy redirect surfaces
// as the 3xx response itself instead of being auto-followed. Following a
// redirect here is actively wrong twice over: Go's default client rewrites
// a 301/302 POST into a GET (issue #198 — the JSON-RPC POST silently
// became a GET and the daemon's POST-only /v1/rpc answered 405 on every
// subcommand), and any follow re-sends the $WA_TOKEN bearer header to the
// redirect target, leaking the credential to whatever host the proxy
// names. We never follow; redirectGuidance turns the 3xx into an
// actionable error.
var noRedirectClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// redirectGuidance converts a non-followed 3xx response into an
// actionable error. The overwhelmingly common cause is pointing --remote
// at http:// behind a proxy that upgrades to https (e.g. nginx 301), so
// when the Location is an https URL we name the exact --remote value to
// use; otherwise we refuse plainly rather than chase a redirect that
// would change the method or leak the bearer token.
func redirectGuidance(resp *http.Response) error {
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if to, err := url.Parse(loc); err == nil && to.Scheme == "https" && to.Host != "" {
		return fmt.Errorf(
			"remote redirected (HTTP %d) to %s — the endpoint requires TLS; re-run with --remote https://%s (a JSON-RPC POST cannot follow a redirect without downgrading to GET or leaking $WA_TOKEN)",
			resp.StatusCode, loc, to.Host)
	}
	return fmt.Errorf(
		"remote redirected (HTTP %d) to %q; refusing to follow — a redirected JSON-RPC POST would downgrade to GET or leak $WA_TOKEN. Point --remote at the final URL directly",
		resp.StatusCode, snippet([]byte(loc)))
}

// callRemote routes a JSON-RPC call over the REST primary adapter
// instead of the unix socket. Spec 110c v0.
//
// Failure-mode mapping (mirrors callAndClose):
//
//	dial / connect failure              → exit 10 (service unavailable)
//	auth failure (401)                  → exit 64 (usage error)
//	transport error                     → exit 1
//	dispatcher error (200 + envelope)   → rpcCodeToExit(envelope.code)
//	HTTP 4xx other                      → exit 64
//	HTTP 5xx                            → exit 70 (internal)
//
// Token can be empty — the daemon's REST adapter will reject with
// 401 unauthorized; that's surfaced as an actionable error.
//
// nolintlint annotation: the function is a flat HTTP request-response
// pipeline; status-code branches map directly to wire semantics, and
// extracting them scatters the audit trail tying each branch to its
// exit-code rule.
//
//nolint:gocyclo // flat status-code translation table; extraction scatters audit
func callRemote(remoteURL, token, method string, params any) (json.RawMessage, int, error) {
	endpoint, err := normaliseRemoteURL(remoteURL)
	if err != nil {
		return nil, 64, err
	}

	if b, ok := params.([]byte); ok {
		params = json.RawMessage(b)
	}
	body := rpcRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  method,
		Params:  params,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 1, fmt.Errorf("marshal request: %w", err)
	}

	// 60s ceiling — long enough for any current dispatcher method
	// (groups list, history search) but short enough to fail fast on
	// a hung daemon.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, 1, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, 10, fmt.Errorf("remote daemon timed out: %w", err)
		}
		return nil, 10, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Codex review §MEDIUM (PR #111): read cap+1 so we can detect
	// oversized bodies and return a specific error instead of silently
	// truncating + producing an ambiguous JSON decode error.
	const respBodyCap = 16 << 20 // 16 MiB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, respBodyCap+1))
	if err != nil {
		return nil, 1, fmt.Errorf("read response: %w", err)
	}
	if len(respBody) > respBodyCap {
		return nil, 70, fmt.Errorf("remote response exceeds %d-byte cap", respBodyCap)
	}

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, 64, redirectGuidance(resp)
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, 64, errors.New("remote auth failed: missing or invalid --token (or $WA_TOKEN); HTTP 401")
	case resp.StatusCode >= 500:
		return nil, 70, fmt.Errorf("remote daemon returned HTTP %d: %s", resp.StatusCode, snippet(respBody))
	case resp.StatusCode >= 400:
		// Try to parse JSON-RPC envelope; fall back to raw status.
		if env := tryDecodeEnvelope(respBody); env != nil && env.Error != nil {
			return nil, rpcCodeToExit(env.Error.Code), env.Error
		}
		return nil, 64, fmt.Errorf("remote daemon returned HTTP %d: %s", resp.StatusCode, snippet(respBody))
	}

	var env rpcResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, 1, fmt.Errorf("unmarshal response: %w (body=%q)", err, snippet(respBody))
	}
	if env.Error != nil {
		return nil, rpcCodeToExit(env.Error.Code), env.Error
	}
	return env.Result, 0, nil
}

// callRemoteUpload POSTs a raw octet-stream body to the daemon's
// /media/upload route (spec 198) and returns the content sha256 the daemon
// computed. It is the upload twin of callRemote: same bearer-auth posture
// (token from $WA_TOKEN, NEVER argv — Codex review §HIGH on PR #111), same
// status-code → exit-code mapping. payload is the already-read file bytes;
// the caller is responsible for the 16 MiB ceiling check (the daemon also
// enforces it and returns 413).
//
// Failure-mode mapping:
//
//	dial / connect failure              → exit 10 (service unavailable)
//	auth failure (401)                  → exit 64 (usage error)
//	body too large (413)                → exit 64 (usage error)
//	HTTP 5xx                            → exit 70 (internal)
//	HTTP 4xx other                      → exit 64
//	transport / decode error            → exit 1
//
//nolint:gocyclo // flat status-code translation table; extraction scatters the audit trail
func callRemoteUpload(remoteURL, token string, payload []byte) (sha string, size int64, exitCode int, err error) {
	endpoint, err := mediaUploadURL(remoteURL)
	if err != nil {
		return "", 0, 64, err
	}

	// 5 min ceiling — a 16 MiB upload over a slow link must not hang forever
	// but is generous enough for the worst case. Mirrors fetchMediaHTTP.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", 0, 1, fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return "", 0, 10, fmt.Errorf("remote daemon timed out: %w", err)
		}
		return "", 0, 10, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", 0, 1, fmt.Errorf("read upload response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		// fall through to decode the sha
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return "", 0, 64, redirectGuidance(resp)
	case resp.StatusCode == http.StatusUnauthorized:
		return "", 0, 64, errors.New("remote auth failed: missing or invalid $WA_TOKEN; HTTP 401")
	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		return "", 0, 64, errors.New("media upload rejected: exceeds the daemon's 16 MiB ceiling; HTTP 413")
	case resp.StatusCode >= 500:
		return "", 0, 70, fmt.Errorf("remote daemon returned HTTP %d: %s", resp.StatusCode, snippet(respBody))
	default:
		if env := tryDecodeEnvelope(respBody); env != nil && env.Error != nil {
			return "", 0, rpcCodeToExit(env.Error.Code), env.Error
		}
		return "", 0, 64, fmt.Errorf("remote daemon returned HTTP %d: %s", resp.StatusCode, snippet(respBody))
	}

	var out struct {
		Schema string `json:"schema"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", 0, 1, fmt.Errorf("unmarshal upload response: %w (body=%q)", err, snippet(respBody))
	}
	if out.SHA256 == "" {
		return "", 0, 1, fmt.Errorf("upload response missing sha256 (body=%q)", snippet(respBody))
	}
	return out.SHA256, out.Size, 0, nil
}

// mediaUploadURL validates the remote URL via the same gate as the RPC
// transport, then swaps the path for /media/upload.
func mediaUploadURL(remoteURL string) (string, error) {
	canonical, err := normaliseRemoteURL(remoteURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(canonical)
	if err != nil {
		return "", fmt.Errorf("parse remote URL: %w", err)
	}
	u.Path = "/media/upload"
	return u.String(), nil
}

// normaliseRemoteURL accepts "https://host", "https://host/", or
// "https://host/v1/rpc" and returns the canonical /v1/rpc endpoint.
// Refuses non-https URLs unless WA_REMOTE_INSECURE=1 is set (operator
// acknowledgement that they accept plaintext over the wire — useful
// for local-loopback testing only).
//
// Codex review §MEDIUM (PR #111): use net/url.Parse with full
// validation (no userinfo, no query, no fragment, path empty or
// /v1/rpc only) to defeat the string-based variants that previously
// accepted shapes like https://host?q= or https://host/v1/rpc/extra.
func normaliseRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("--remote must be an absolute URL (https://host[/v1/rpc])")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("--remote URL parse: %w", err)
	}
	if u.Host == "" {
		return "", errors.New("--remote must be an absolute URL with a host (https://host[/v1/rpc])")
	}
	if u.User != nil {
		return "", errors.New("--remote URL must not include user:password (use $WA_TOKEN bearer auth)")
	}
	if u.RawQuery != "" {
		return "", errors.New("--remote URL must not include a query string")
	}
	if u.Fragment != "" {
		return "", errors.New("--remote URL must not include a fragment")
	}
	switch u.Scheme {
	case "https":
		// ok
	case "http":
		if !insecureRemoteAllowed() {
			return "", errors.New("--remote uses plaintext http://; set WA_REMOTE_INSECURE=1 to acknowledge (or use https://)")
		}
	default:
		return "", fmt.Errorf("--remote scheme %q is not supported (use https:// or http://)", u.Scheme)
	}
	path := strings.TrimSuffix(u.Path, "/")
	switch path {
	case "", "/v1/rpc":
		// canonicalise — ignore the original path and always end in /v1/rpc
	default:
		return "", fmt.Errorf("--remote URL path must be empty or /v1/rpc (got %q)", u.Path)
	}
	canonical := url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   "/v1/rpc",
	}
	return canonical.String(), nil
}

func insecureRemoteAllowed() bool {
	return os.Getenv("WA_REMOTE_INSECURE") == "1"
}

// snippet returns at most n bytes of body for error-message display,
// with control characters replaced by '?' so a malicious or
// compromised remote cannot inject ANSI/control sequences into the
// operator's terminal. Codex review §MEDIUM (PR #111).
func snippet(b []byte) string {
	const n = 200
	in := b
	if len(in) > n {
		in = in[:n]
	}
	var sb strings.Builder
	sb.Grow(len(in))
	for _, r := range string(in) {
		switch {
		case r == '\n', r == '\t', r == ' ':
			sb.WriteRune(r)
		case unicode.IsControl(r), !unicode.IsPrint(r):
			sb.WriteRune('?')
		default:
			sb.WriteRune(r)
		}
	}
	if len(b) > n {
		sb.WriteString("...")
	}
	return sb.String()
}

func tryDecodeEnvelope(raw []byte) *rpcResponse {
	var env rpcResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	return &env
}
