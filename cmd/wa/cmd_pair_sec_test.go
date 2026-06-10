package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteLoadingHTMLDefeatsSymlink pins SEC-03: a pre-planted
// symlink at the pair-HTML path must not redirect the write — the
// symlink is replaced by a regular file and its target stays intact.
func TestWriteLoadingHTMLDefeatsSymlink(t *testing.T) {
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

	if err := writeLoadingHTML(link); err != nil {
		t.Fatalf("writeLoadingHTML: %v", err)
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
	if len(body) == 0 {
		t.Fatal("loading HTML not written")
	}
}
