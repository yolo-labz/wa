package execguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeBinary(t *testing.T, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Chmod separately: WriteFile's perm is filtered by umask.
	if err := os.Chmod(p, perm); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyAcceptsOwnedNonWritable(t *testing.T) {
	t.Parallel()
	for _, perm := range []os.FileMode{0o700, 0o755, 0o500} {
		if err := Verify(writeBinary(t, perm)); err != nil {
			t.Errorf("perm %04o: %v, want nil", perm, err)
		}
	}
}

func TestVerifyRejectsGroupWorldWritable(t *testing.T) {
	t.Parallel()
	for _, perm := range []os.FileMode{0o775, 0o757, 0o777} {
		err := Verify(writeBinary(t, perm))
		if !errors.Is(err, ErrUntrusted) {
			t.Errorf("perm %04o: err = %v, want ErrUntrusted", perm, err)
		}
	}
}

func TestVerifyRejectsNonRegular(t *testing.T) {
	t.Parallel()
	if err := Verify(t.TempDir()); !errors.Is(err, ErrUntrusted) {
		t.Errorf("directory: err = %v, want ErrUntrusted", err)
	}
}

func TestVerifyMissingPath(t *testing.T) {
	t.Parallel()
	err := Verify(filepath.Join(t.TempDir(), "absent"))
	if err == nil || errors.Is(err, ErrUntrusted) {
		t.Errorf("missing: err = %v, want plain stat error", err)
	}
}

// TestVerifyFollowsSymlink pins the deliberate Stat (not Lstat) choice:
// a symlink to a trusted binary passes — execve runs the target, and
// the target is what the ownership/mode check must apply to.
func TestVerifyFollowsSymlink(t *testing.T) {
	t.Parallel()
	target := writeBinary(t, 0o755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Verify(link); err != nil {
		t.Errorf("symlink to trusted target: %v, want nil", err)
	}

	bad := writeBinary(t, 0o777)
	badLink := filepath.Join(t.TempDir(), "badlink")
	if err := os.Symlink(bad, badLink); err != nil {
		t.Fatal(err)
	}
	if err := Verify(badLink); !errors.Is(err, ErrUntrusted) {
		t.Errorf("symlink to writable target: err = %v, want ErrUntrusted", err)
	}
}

// Owner-mismatch branch (uid != 0 && uid != euid) is not exercised:
// creating a file owned by another uid requires privileges tests don't
// have. The branch is two lines reading a Stat_t field.
