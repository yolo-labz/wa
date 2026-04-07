package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyRuntimeParent_Accepts0700EuidOwned(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wa")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := verifyRuntimeParent(sub); err != nil {
		t.Errorf("0700 euid-owned dir rejected: %v", err)
	}
}

func TestVerifyRuntimeParent_RejectsMode0755(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wa")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	err := verifyRuntimeParent(sub)
	if err == nil {
		t.Fatal("0755 dir accepted, want rejection")
	}
	if !errors.Is(err, ErrRuntimeDirInsecure) {
		t.Errorf("error = %v, want ErrRuntimeDirInsecure", err)
	}
}

func TestVerifyRuntimeParent_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := verifyRuntimeParent(link)
	if err == nil {
		t.Fatal("symlink accepted, want rejection")
	}
	if !errors.Is(err, ErrRuntimeDirInsecure) {
		t.Errorf("error = %v, want ErrRuntimeDirInsecure", err)
	}
}

func TestVerifyRuntimeParent_RejectsMissing(t *testing.T) {
	err := verifyRuntimeParent("/nonexistent/path/12345")
	if err == nil {
		t.Fatal("missing path accepted, want rejection")
	}
	if !errors.Is(err, ErrRuntimeDirInsecure) {
		t.Errorf("error = %v, want ErrRuntimeDirInsecure", err)
	}
}

func TestVerifyRuntimeParent_RejectsRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "regular")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	err := verifyRuntimeParent(file)
	if err == nil {
		t.Fatal("regular file accepted, want rejection")
	}
	if !errors.Is(err, ErrRuntimeDirInsecure) {
		t.Errorf("error = %v, want ErrRuntimeDirInsecure", err)
	}
}
