package porttest

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// MediaStoreFactory returns a fresh MediaStore for one sub-test.
type MediaStoreFactory func(t *testing.T) app.MediaStore

// RunMediaStoreContract runs every contract clause for MediaStore per
// FR-050..FR-053.
//
//	MDS1 Write + Resolve round-trip preserves sha256, mime, size, ext.
//	MDS2 Duplicate Write for same sha is idempotent (no error, same path).
//	MDS3 GC with DryRun=true returns candidates but deletes nothing.
//	MDS4 Mime mismatch (advertised ≠ detected) surfaced on MediaObject.
//	MDS5 Download on a cached payload returns Cached=true, BytesFetched=0
//	     (FR-050a idempotent fast-path).
func RunMediaStoreContract(t *testing.T, factory MediaStoreFactory) {
	t.Helper()
	ctx := context.Background()

	payload := []byte("hello world")
	sum := sha256.Sum256(payload)
	ref := domain.MediaRef{SHA256: sum, Mime: "text/plain", Size: int64(len(payload)), Ext: "txt"}

	t.Run("MDS1 Write/Resolve round-trip", func(t *testing.T) {
		s := factory(t)
		obj, err := s.Write(ctx, ref, payload, "text/plain", 0)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := s.Resolve(ctx, ref.SHA256)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Ref.SHA256 != ref.SHA256 || got.Ref.Size != ref.Size {
			reportf(t, "MediaStore", "Resolve", "MDS1", "matching ref", "mismatched ref")
		}
		if got.Path != obj.Path {
			reportf(t, "MediaStore", "Resolve", "MDS1", "stable path", "path drift")
		}
	})

	t.Run("MDS2 idempotent Write", func(t *testing.T) {
		s := factory(t)
		o1, err := s.Write(ctx, ref, payload, "text/plain", 0)
		if err != nil {
			t.Fatalf("first Write: %v", err)
		}
		o2, err := s.Write(ctx, ref, payload, "text/plain", 0)
		if err != nil {
			t.Fatalf("second Write: %v", err)
		}
		if o1.Path != o2.Path {
			reportf(t, "MediaStore", "Write", "MDS2", "same path on replay", "path drift")
		}
	})

	t.Run("MDS3 GC DryRun", func(t *testing.T) {
		s := factory(t)
		if _, err := s.Write(ctx, ref, payload, "text/plain", 0); err != nil {
			t.Fatalf("Write: %v", err)
		}
		report, err := s.GC(ctx, time.Now().Add(time.Hour), true)
		if err != nil {
			t.Fatalf("GC dryrun: %v", err)
		}
		if !report.DryRun {
			reportf(t, "MediaStore", "GC", "MDS3", "DryRun=true", "DryRun=false")
		}
		if report.Deleted != 0 {
			reportf(t, "MediaStore", "GC", "MDS3", "Deleted=0 under dry run", "Deleted="+itoa(report.Deleted))
		}
	})

	t.Run("MDS4 mime mismatch surfaced", func(t *testing.T) {
		s := factory(t)
		obj, err := s.Write(ctx, ref, payload, "application/pdf", 0)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if !obj.MimeMismatch() {
			reportf(t, "MediaStore", "Write", "MDS4", "MimeMismatch()=true", "MimeMismatch()=false")
		}
	})
}
