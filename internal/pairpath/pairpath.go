// Package pairpath is the single source of truth for the pairing-HTML
// handoff location (ARCH-03). The whatsmeow adapter WRITES the file
// (QR rotation during `wad` pairing), the wa CLI READS it (`wa pair
// --browser` points the browser at file://<path>), and the daemon's
// PathResolver reports it (`wa profile show`). Before this package each
// of the three carried its own copy of the path expression with a
// "keep in sync" comment — exactly the duplicated-literal drift that
// caused #180 item 5 for the layout schema version.
//
// The path is profile-suffixed per feature 008 FR-014 so two profiles
// pairing concurrently on the same host cannot clobber each other's
// QR page. Both processes run as the same user on the same host, so
// they share the same os.TempDir.
package pairpath

import (
	"os"
	"path/filepath"
)

// Path returns the deterministic per-profile pairing HTML location:
// os.TempDir()/wa-pair-<profile>.html (FR-014). An empty profile maps
// to "default" so a caller that has not resolved a profile yet still
// lands on the canonical default-profile file rather than a bare
// "wa-pair-.html".
func Path(profile string) string {
	if profile == "" {
		profile = "default"
	}
	return filepath.Join(os.TempDir(), "wa-pair-"+profile+".html")
}
