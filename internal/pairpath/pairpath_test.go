package pairpath_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/pairpath"
)

// TestPathProfileSuffix pins FR-014: the file name embeds the profile so
// concurrent pairing across profiles cannot collide.
func TestPathProfileSuffix(t *testing.T) {
	got := pairpath.Path("work")
	want := filepath.Join(os.TempDir(), "wa-pair-work.html")
	if got != want {
		t.Errorf("Path(\"work\") = %q, want %q", got, want)
	}
}

// TestPathEmptyProfileFallsBackToDefault pins the empty-profile guard:
// an unresolved profile lands on the canonical default-profile file,
// never a malformed "wa-pair-.html".
func TestPathEmptyProfileFallsBackToDefault(t *testing.T) {
	got := pairpath.Path("")
	if !strings.HasSuffix(got, "wa-pair-default.html") {
		t.Errorf("Path(\"\") = %q, want wa-pair-default.html suffix", got)
	}
	if got != pairpath.Path("default") {
		t.Errorf("Path(\"\") = %q diverges from Path(\"default\") = %q", got, pairpath.Path("default"))
	}
}

// TestPathStaysInTempDir pins that the path never escapes os.TempDir —
// the CLI hands the value straight to a browser open call.
func TestPathStaysInTempDir(t *testing.T) {
	got := pairpath.Path("burocracy")
	if filepath.Dir(got) != filepath.Clean(os.TempDir()) {
		t.Errorf("Path dir = %q, want %q", filepath.Dir(got), os.TempDir())
	}
}
