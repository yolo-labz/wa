package whatsmeow

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"github.com/skip2/go-qrcode"
)

// PairHTMLPath returns the filesystem path where the pairing HTML file
// is written. The path is deterministic so `wa pair --browser` can
// point the browser at file://<PairHTMLPath()>.
func PairHTMLPath() string {
	return filepath.Join(os.TempDir(), "wa-pair.html")
}

var pairHTMLTemplate = template.Must(template.New("pair").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="3">
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
  .success {
    font-size: 2.5rem; margin: 1rem 0;
    color: #22c55e;
  }
  .success-msg { font-size: 1.05rem; color: #22c55e; font-weight: 600; margin-bottom: 0.5rem; }
  .hint { font-size: 0.8125rem; color: #737373; margin-top: 1.5rem; }
  .code { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: 0.75rem; color: #737373; }
</style>
</head>
<body>
  <div class="card">
{{- if .Paired}}
    <div class="success">✓</div>
    <div class="success-msg">Paired successfully</div>
    <p class="hint">You can close this tab.</p>
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

// writeQRHTML atomically writes the pairing HTML file. If paired is true,
// the file renders a success message instead of a QR code.
func writeQRHTML(code string, paired bool) error {
	var b64 string
	if !paired {
		png, err := qrcode.Encode(code, qrcode.Medium, 320)
		if err != nil {
			return fmt.Errorf("qr encode: %w", err)
		}
		b64 = base64.StdEncoding.EncodeToString(png)
	}

	path := PairHTMLPath()
	tmp := path + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec // tmp path derived from os.TempDir()
	if err != nil {
		return err
	}

	if err := pairHTMLTemplate.Execute(f, struct {
		QR     string
		Paired bool
	}{b64, paired}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
