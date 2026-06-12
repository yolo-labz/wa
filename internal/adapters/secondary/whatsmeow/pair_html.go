package whatsmeow

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/skip2/go-qrcode"

	"github.com/yolo-labz/wa/v2/internal/pairhtml"
	"github.com/yolo-labz/wa/v2/internal/pairpath"
)

// writeQRHTML atomically writes the pairing HTML file for the given
// profile (pairpath.Path — FR-014 per-profile suffix). If paired is
// true, the file renders a success message instead of a QR code.
// Template + SEC-03 exclusive open are single-sourced in
// internal/pairhtml (ARCH-02); this writer keeps the QR encoding and
// the tmp+rename atomicity, which the CLI's loading write doesn't need.
func writeQRHTML(profile, code string, paired bool) error {
	state := pairhtml.StatePaired
	var b64 string
	if !paired {
		png, err := qrcode.Encode(code, qrcode.Medium, 320)
		if err != nil {
			return fmt.Errorf("qr encode: %w", err)
		}
		b64 = base64.StdEncoding.EncodeToString(png)
		state = pairhtml.StateQR
	}

	doc, err := pairhtml.Render(state, b64)
	if err != nil {
		return err
	}

	path := pairpath.Path(profile)
	tmp := path + ".tmp"
	// SEC-03: the tmp name is predictable inside the shared os.TempDir,
	// so a hostile local user could pre-plant a symlink and redirect the
	// write. pairhtml.OpenExclusive refuses any pre-existing path
	// (symlinks included), removing a stale tmp from a crashed run once.
	f, err := pairhtml.OpenExclusive(tmp)
	if err != nil {
		return err
	}

	if _, err := f.WriteString(doc); err != nil {
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
