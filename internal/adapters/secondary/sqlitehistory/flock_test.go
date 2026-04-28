package sqlitehistory_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

func TestFlockSingleOpenOK(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestFlockSecondOpenBlocks(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	first, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		s, err := sqlitehistory.Open(context.Background(), dbPath)
		if s != nil {
			_ = s.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		_ = first.Close()
		t.Fatalf("second Open returned while first still held lock: %v", err)
	case <-time.After(150 * time.Millisecond):
		// Good: second Open is blocked on the lockedfile mutex.
	}
	// Release the lock and drain the goroutine before TempDir cleanup runs;
	// otherwise the unblocked Open creates WAL files racing the cleanup.
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Open never unblocked after first Close")
	}
}

func TestFlockSecondOpenAfterClose(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	first, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	second, err := sqlitehistory.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open after Close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
