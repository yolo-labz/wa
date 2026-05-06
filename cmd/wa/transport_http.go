package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, 10, fmt.Errorf("remote daemon timed out: %w", err)
		}
		return nil, 10, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		return nil, 1, fmt.Errorf("read response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, 64, fmt.Errorf("remote auth failed: missing or invalid --token (or $WA_TOKEN); HTTP 401")
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

// normaliseRemoteURL accepts "https://host", "https://host/", or
// "https://host/v1/rpc" and returns the canonical /v1/rpc endpoint.
// Refuses non-https URLs unless WA_REMOTE_INSECURE=1 is set (operator
// acknowledgement that they accept plaintext over the wire — useful
// for local-loopback testing only).
func normaliseRemoteURL(raw string) (string, error) {
	raw = strings.TrimSuffix(raw, "/")
	switch {
	case strings.HasSuffix(raw, "/v1/rpc"):
		return raw, nil
	case strings.HasPrefix(raw, "https://"):
		return raw + "/v1/rpc", nil
	case strings.HasPrefix(raw, "http://"):
		// Plaintext is a footgun on the public internet. Allow only
		// when the operator explicitly opts in.
		if !insecureRemoteAllowed() {
			return "", fmt.Errorf("--remote uses plaintext http://; set WA_REMOTE_INSECURE=1 to acknowledge (or use https://)")
		}
		return raw + "/v1/rpc", nil
	default:
		return "", fmt.Errorf("--remote must be an absolute URL (https://host[/v1/rpc])")
	}
}

func insecureRemoteAllowed() bool {
	return os.Getenv("WA_REMOTE_INSECURE") == "1"
}

// snippet returns at most n bytes of body for error-message display.
func snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

func tryDecodeEnvelope(raw []byte) *rpcResponse {
	var env rpcResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	return &env
}
