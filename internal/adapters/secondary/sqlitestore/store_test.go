package sqlitestore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
)

func TestOpenHappyPath(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "session.db")

	store, err := sqlitestore.Open(context.Background(), dbPath, nil)
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	if store.Container() == nil {
		t.Fatal("Container(): want non-nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
}

func TestOpenSecondFailsWithLockContention(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "session.db")

	first, err := sqlitestore.Open(context.Background(), dbPath, nil)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	// lockedfile.Edit blocks; use a context with a tight deadline to
	// detect contention deterministically. We run Open in a goroutine
	// and assert it does not return before the deadline.
	//
	// Timeout was 150ms; observed flakes on the dokku self-hosted
	// runner (PR #96 CI). 500ms gives headroom for slow scheduler
	// startup without changing the test's intent (verify the second
	// Open BLOCKS rather than returning quickly).
	done := make(chan error, 1)
	go func() {
		s, err := sqlitestore.Open(context.Background(), dbPath, nil)
		if s != nil {
			_ = s.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("second Open returned while first still held lock: err=%v", err)
	case <-time.After(500 * time.Millisecond):
		// Good: second Open is blocked on the lockedfile mutex.
	}

	// Release the lock and drain the goroutine BEFORE t.TempDir
	// cleanup runs. Otherwise the unblocked second Open creates files
	// in the temp dir concurrently with RemoveAll → flaky
	// `unlinkat: directory not empty` on the dokku runner.
	if err := first.Close(); err != nil {
		t.Fatalf("first.Close: %v", err)
	}
	<-done
}

func TestCloseReleasesLock(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "session.db")

	first, err := sqlitestore.Open(context.Background(), dbPath, nil)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := sqlitestore.Open(context.Background(), dbPath, nil)
	if err != nil {
		t.Fatalf("second Open after Close: unexpected error: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
