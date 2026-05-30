package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestGCSweepsUploadedMedia confirms media persisted via the same Write
// path the POST /media/upload route uses (#199) is reclaimed by GC once
// its LastAccessAt ages past the cutoff — i.e. uploaded-for-send objects
// are ordinary sha-keyed cache entries with no GC exemption (#202).
func TestGCSweepsUploadedMedia(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Pin the clock in the past so the object's LastAccessAt is stale
	// relative to the cutoff we pass to GC below.
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := &FakeClock{T: past}

	store, err := NewMediaStore(t.TempDir(), clk)
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}

	// Exactly what handleMediaUpload does: content-address the payload and
	// Write it through the MediaWriter (== this store).
	payload := []byte("uploaded-for-send bytes")
	sum := sha256.Sum256(payload)
	ref := domain.MediaRef{SHA256: sum, Mime: "text/plain", Size: int64(len(payload)), Ext: "txt"}
	obj, err := store.Write(ctx, ref, payload, "text/plain", 0)
	if err != nil {
		t.Fatalf("Write (upload path): %v", err)
	}
	if _, err := os.Stat(obj.Path); err != nil {
		t.Fatalf("uploaded object missing on disk: %v", err)
	}

	// A dry run must list the uploaded object as a candidate without
	// deleting it.
	cutoff := past.Add(time.Hour)
	dry, err := store.GC(ctx, cutoff, true)
	if err != nil {
		t.Fatalf("GC dry run: %v", err)
	}
	if dry.Candidates != 1 || dry.Deleted != 0 {
		t.Fatalf("dry-run GC: candidates=%d deleted=%d, want 1/0", dry.Candidates, dry.Deleted)
	}
	if _, err := os.Stat(obj.Path); err != nil {
		t.Fatalf("dry-run GC must not delete uploaded object: %v", err)
	}

	// A real sweep reclaims it.
	rep, err := store.GC(ctx, cutoff, false)
	if err != nil {
		t.Fatalf("GC sweep: %v", err)
	}
	if rep.Deleted != 1 {
		t.Fatalf("GC swept %d objects, want 1 (uploaded media must be collected)", rep.Deleted)
	}
	if rep.BytesFreed != ref.Size {
		t.Fatalf("GC freed %d bytes, want %d", rep.BytesFreed, ref.Size)
	}
	if _, err := os.Stat(obj.Path); !os.IsNotExist(err) {
		t.Fatalf("uploaded object should be gone after sweep, stat err=%v", err)
	}
	if _, err := store.Resolve(ctx, ref.SHA256); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve after sweep should miss, got err=%v", err)
	}
}
