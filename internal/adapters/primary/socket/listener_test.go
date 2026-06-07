package socket

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestListen_SocketCreated0600 pins the core perm guarantee: the bound
// socket file ends up mode 0600 and its parent is 0700. This is what makes
// the umask-free bind safe (see listen()/validateParentDir Check 5b).
func TestListen_SocketCreated0600(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	sock := filepath.Join(parent, "w.sock")

	ln, err := listen(context.Background(), sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	if m := mustPerm(t, sock); m != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", m)
	}
	if m := mustPerm(t, parent); m != 0o700 {
		t.Errorf("parent mode = %04o, want 0700", m)
	}
}

// TestListen_HealsTraversableParent is the regression test for the fix that
// replaced the process-global umask narrowing (which raced concurrent
// tests' t.TempDir under -shuffle, EACCES) with parent-dir enforcement: a
// pre-existing parent that is traversable-but-not-writable (0755) — which
// passes the group/world-WRITE check — must be self-healed to 0700 so no
// other uid can reach the socket during the bind→Chmod window. listen()
// must still succeed and the socket must still be 0600.
func TestListen_HealsTraversableParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	// Confirm the precondition: 0755 is traversable by other uids.
	if m := mustPerm(t, parent); m != 0o755 {
		t.Fatalf("precondition: parent mode = %04o, want 0755", m)
	}
	sock := filepath.Join(parent, "w.sock")

	ln, err := listen(context.Background(), sock)
	if err != nil {
		t.Fatalf("listen with 0755 parent: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	if m := mustPerm(t, parent); m&0o077 != 0 {
		t.Errorf("parent mode = %04o after listen, want no group/other bits (0700)", m)
	}
	if m := mustPerm(t, sock); m != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", m)
	}
}

// TestListen_RejectsWritableParent keeps the Check 4 guarantee: a
// group/world-WRITABLE parent is rejected outright (not healed), because a
// writable parent lets another uid swap the socket regardless of its mode.
func TestListen_RejectsWritableParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(parent, 0o777); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Chmod(parent, 0o777); err != nil { // MkdirAll honours umask; force it
		t.Fatalf("chmod parent 0777: %v", err)
	}
	sock := filepath.Join(parent, "w.sock")

	_, err := listen(context.Background(), sock)
	if err == nil {
		t.Fatal("listen with world-writable parent: want error, got nil")
	}
	if !errors.Is(err, ErrParentWorldWritable) {
		t.Errorf("want ErrParentWorldWritable, got %v", err)
	}
}

func mustPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}
