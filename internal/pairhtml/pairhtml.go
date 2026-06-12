// Package pairhtml single-sources the pair-page presentation: the one
// HTML template (loading / QR / paired states) and the SEC-03-hardened
// exclusive file open both writers need. Before this package the thin
// CLI carried its own inline loading document and the whatsmeow adapter
// carried a second, near-identical QR document (ARCH-02: UI logic in
// the thin-client binary, duplicated against the adapter). The path
// half of the same story is internal/pairpath (ARCH-03, PR #261).
//
// Writer split: the CLI writes the LOADING state directly at
// pairpath.Path before it opens the browser and dials the pair RPC;
// the adapter renders QR/PAIRED states into a tmp sibling and renames.
// Both go through OpenExclusive so a pre-planted symlink in the shared
// temp dir is replaced, never followed (SEC-03, PR #234).
package pairhtml

import (
	"fmt"
	"html/template"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// State selects which of the three pair-page bodies Render emits.
type State int

const (
	// StateLoading is the pre-QR placeholder the CLI writes so the
	// browser has content while the pair RPC spins up. Refreshes every
	// second to pick the first QR up quickly.
	StateLoading State = iota
	// StateQR renders the scannable code (expects the base64 PNG).
	StateQR
	// StatePaired renders the success card.
	StatePaired
)

// pageTemplate is the single pair-page document. One style block, one
// card layout, three bodies — the loading and QR states previously
// drifted apart across two packages.
var pageTemplate = template.Must(template.New("pair").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
{{- if .Refresh}}
<meta http-equiv="refresh" content="{{.Refresh}}">
{{- end}}
<title>wa pair</title>
<style>
  :root { color-scheme: dark; }
  html, body { height: 100%; margin: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    background: #0a0a0a; color: #e5e5e5;
    display: flex; align-items: center; justify-content: center;
  }
  .card {
    background: #141414;
    border: 1px solid #262626;
    padding: 2.5rem 3rem;
    border-radius: 14px;
    text-align: center;
    max-width: 420px;
  }
  h1 { margin: 0 0 1.25rem; font-size: 1.15rem; font-weight: 600; letter-spacing: -0.01em; }
  .qr {
    background: #fff;
    padding: 1rem;
    border-radius: 10px;
    display: inline-block;
    line-height: 0;
  }
  .qr img { display: block; width: 320px; height: 320px; image-rendering: pixelated; }
  .steps { margin: 1.5rem 0 0; padding: 0; list-style: none; text-align: left; font-size: 0.875rem; color: #a3a3a3; }
  .steps li { margin: 0.5rem 0; }
  .steps b { color: #e5e5e5; font-weight: 600; }
  .spinner { display: inline-block; width: 32px; height: 32px; border: 3px solid #262626;
    border-top-color: #4ade80; border-radius: 50%; animation: spin 0.8s linear infinite; margin: 1rem; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .success {
    font-size: 2.5rem; margin: 1rem 0;
    color: #22c55e;
  }
  .success-msg { font-size: 1.05rem; color: #22c55e; font-weight: 600; margin-bottom: 0.5rem; }
  .hint { font-size: 0.8125rem; color: #737373; margin-top: 1.5rem; }
</style>
</head>
<body>
  <div class="card">
{{- if .Paired}}
    <div class="success">✓</div>
    <div class="success-msg">Paired successfully</div>
    <p class="hint">You can close this tab.</p>
{{- else if .Loading}}
    <h1>Connecting to WhatsApp…</h1>
    <div class="spinner"></div>
    <p class="hint">The QR code will appear here in a moment.</p>
{{- else}}
    <h1>Scan with WhatsApp</h1>
    <div class="qr"><img src="data:image/png;base64,{{.QR}}" alt="QR code"></div>
    <ol class="steps">
      <li>Open <b>WhatsApp</b> on your phone</li>
      <li><b>Settings → Linked Devices</b></li>
      <li>Tap <b>Link a Device</b></li>
      <li>Scan the code above</li>
    </ol>
    <p class="hint">Auto-refreshes every 3s. QR rotates every 20s.</p>
{{- end}}
  </div>
</body>
</html>
`))

// Render returns the pair-page document for state. qrB64 is the base64
// PNG payload and is only consulted for StateQR.
func Render(state State, qrB64 string) (string, error) {
	data := struct {
		Loading bool
		Paired  bool
		QR      string
		Refresh int
	}{}
	switch state {
	case StateLoading:
		data.Loading = true
		data.Refresh = 1
	case StateQR:
		data.QR = qrB64
		data.Refresh = 3
	case StatePaired:
		data.Paired = true
	default:
		return "", fmt.Errorf("pairhtml: unknown state %d", state)
	}
	var sb strings.Builder
	if err := pageTemplate.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// OpenExclusive creates path exclusively (0600, no symlink following),
// retrying once after removing a stale leftover. SEC-03: O_EXCL refuses
// ANY pre-existing path — a pre-planted symlink in the shared temp dir
// is replaced, never followed — and O_NOFOLLOW belts the final
// component.
func OpenExclusive(path string) (*os.File, error) {
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY | unix.O_NOFOLLOW
	f, err := os.OpenFile(path, flags, 0o600) //nolint:gosec // G304: path derived from os.TempDir via pairpath, not user input
	if err == nil {
		return f, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	if rmErr := os.Remove(path); rmErr != nil {
		return nil, rmErr
	}
	return os.OpenFile(path, flags, 0o600) //nolint:gosec // see above
}

// WriteFile renders state and writes it to path via OpenExclusive.
// Convenience for the CLI's direct (non-atomic) loading write; the
// adapter composes OpenExclusive with its own tmp+rename for atomicity.
func WriteFile(path string, state State, qrB64 string) error {
	doc, err := Render(state, qrB64)
	if err != nil {
		return err
	}
	f, err := OpenExclusive(path)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(doc); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
