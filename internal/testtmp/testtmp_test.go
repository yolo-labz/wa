package testtmp

import (
	"os"
	"strings"
	"testing"
)

func TestShortenLongTMPDIR(t *testing.T) {
	long := t.TempDir() + "/" + strings.Repeat("x", 90)
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)

	cleanup := Shorten()
	got := os.TempDir()
	if len(got) > maxBase {
		t.Fatalf("TempDir() = %q (%d bytes), want <= %d", got, len(got), maxBase)
	}
	cleanup()
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("short dir %q not removed: %v", got, err)
	}
	if os.TempDir() != long {
		t.Errorf("TMPDIR not restored: %q", os.TempDir())
	}
}

func TestShortenLeavesShortTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	Shorten()()
	if os.TempDir() != "/tmp" {
		t.Errorf("TempDir() = %q, want /tmp", os.TempDir())
	}
}
