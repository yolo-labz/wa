package pairhtml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderStates pins the three bodies of the single template: each
// state carries its distinctive markup and the right refresh cadence.
func TestRenderStates(t *testing.T) {
	t.Parallel()

	loading, err := Render(StateLoading, "")
	if err != nil {
		t.Fatalf("Render(loading): %v", err)
	}
	for _, want := range []string{"Connecting to WhatsApp", "spinner", `refresh" content="1"`} {
		if !strings.Contains(loading, want) {
			t.Errorf("loading page missing %q", want)
		}
	}

	qr, err := Render(StateQR, "QkFTRTY0")
	if err != nil {
		t.Fatalf("Render(qr): %v", err)
	}
	for _, want := range []string{"Scan with WhatsApp", "data:image/png;base64,QkFTRTY0", "Linked Devices", `refresh" content="3"`} {
		if !strings.Contains(qr, want) {
			t.Errorf("qr page missing %q", want)
		}
	}

	paired, err := Render(StatePaired, "")
	if err != nil {
		t.Fatalf("Render(paired): %v", err)
	}
	if !strings.Contains(paired, "Paired successfully") {
		t.Error("paired page missing success message")
	}
	if strings.Contains(paired, "http-equiv") {
		t.Error("paired page must not auto-refresh")
	}

	if _, err := Render(State(99), ""); err == nil {
		t.Error("unknown state must error")
	}
}

// TestOpenExclusiveDefeatsSymlink pins SEC-03: a pre-planted symlink at
// the pair-HTML path must not redirect the write — the symlink is
// replaced by a regular file and its target stays intact.
func TestOpenExclusiveDefeatsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	if err := os.WriteFile(target, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "wa-pair.html")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(link, StateLoading, ""); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "precious" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("path is still a symlink (mode %v, err %v)", fi.Mode(), err)
	}
	body, _ := os.ReadFile(link)
	if !strings.Contains(string(body), "Connecting to WhatsApp") {
		t.Fatal("loading HTML not written through the hardened open")
	}
}

// TestWriteFilePerms pins the 0600 mode on the created page (the QR is
// a session-grade secret while it is valid).
func TestWriteFilePerms(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wa-pair.html")
	if err := WriteFile(path, StateQR, "QkFTRTY0"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}
