package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDataRoot_Basic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	reloadXDG(t)

	// Create the wa directory.
	if err := os.MkdirAll(filepath.Join(root, "wa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "wa", "marker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writefile: %v", err)
	}

	exists, err := statInDataRoot("marker")
	if err != nil {
		t.Fatalf("statInDataRoot: %v", err)
	}
	if !exists {
		t.Error("marker file not found via os.Root")
	}
}

func TestStatInDataRoot_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	reloadXDG(t)

	_ = os.MkdirAll(filepath.Join(root, "wa"), 0o700)
	// Plant a file OUTSIDE the data root. A traversal-vulnerable
	// implementation would find it.
	outside := filepath.Join(root, "outside")
	_ = os.WriteFile(outside, []byte("x"), 0o600)

	// Ask for ../outside — must be rejected or not found.
	exists, err := statInDataRoot("../outside")
	if exists {
		t.Error("os.Root let a ../traversal escape the root")
	}
	// Either an error or a clean "not found" is acceptable; both imply
	// the Root correctly refused the traversal.
	_ = err
}

func TestStatInDataRoot_MissingRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/nonexistent/path/deadbeef")
	reloadXDG(t)
	exists, err := statInDataRoot("anything")
	if err != nil {
		t.Errorf("missing root should return (false, nil), got err=%v", err)
	}
	if exists {
		t.Error("exists should be false for missing root")
	}
}
