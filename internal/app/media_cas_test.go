package app

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

func TestMediaRootRelative(t *testing.T) {
	r, err := NewMediaRoot("/tmp/media")
	if err != nil {
		t.Fatalf("NewMediaRoot: %v", err)
	}
	ref := domain.MediaRef{
		SHA256: [32]byte{0xab, 0xcd, 0xef},
		Mime:   "image/jpeg",
		Size:   1,
		Ext:    "jpg",
	}
	abs := r.AbsolutePath(ref)
	if abs != "/tmp/media/ab/cdef0000000000000000000000000000000000000000000000000000000000.jpg" {
		t.Fatalf("AbsolutePath: got %q", abs)
	}
}

func TestMediaRootRejectsRelativeRoot(t *testing.T) {
	if _, err := NewMediaRoot("relative/root"); err == nil {
		t.Fatalf("want error, got nil")
	}
	if _, err := NewMediaRoot(""); err == nil {
		t.Fatalf("want error for empty, got nil")
	}
}

func TestMediaDownloadConcurrencyCap(t *testing.T) {
	sem := NewDownloadSemaphore(2)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	r1, err := sem.Acquire(ctx.Done())
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	r2, err := sem.Acquire(ctx.Done())
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if sem.InFlight() != 2 {
		t.Fatalf("InFlight: got %d want 2", sem.InFlight())
	}
	// Third acquire should block until ctx expires.
	done := make(chan error, 1)
	go func() {
		release, err := sem.Acquire(ctx.Done())
		if err == nil {
			release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("third acquire: want cancel error, got success")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("third acquire did not unblock after ctx timeout")
	}
	r1()
	r2()
	if sem.InFlight() != 0 {
		t.Fatalf("InFlight after releases: got %d want 0", sem.InFlight())
	}
}

func TestMediaDownloadSemDefaultCap(t *testing.T) {
	sem := NewDownloadSemaphore(0)
	if sem.Cap() != DefaultDownloadConcurrency {
		t.Fatalf("default cap: got %d want %d", sem.Cap(), DefaultDownloadConcurrency)
	}
}
