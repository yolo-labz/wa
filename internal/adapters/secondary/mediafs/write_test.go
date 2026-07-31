// This package must keep at least one test file. With none, `go test
// -covermode=atomic -coverprofile ./...` (what CI runs) fails to link the
// cmd/wad test binary with:
//
//	link: fingerprint mismatch: .../secondary/whatsmeow has <a>,
//	import from .../cmd/wad expecting <b>
//
// Reproduced deterministically on go1.26.5 with a cold GOCACHE, and only
// with coverage on (plain `go test -race ./...` is green either way).
// Deleting these tests brings the build failure back.
package mediafs_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/mediafs"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// refFor builds a well-formed MediaRef whose digest actually matches payload,
// so a test that wants the happy path does not accidentally exercise the
// mismatch guard.
func refFor(payload []byte) domain.MediaRef {
	return domain.MediaRef{
		SHA256: sha256.Sum256(payload),
		Mime:   "image/png",
		Size:   int64(len(payload)),
		Ext:    "png",
	}
}

func TestValidateWriteRejectsDigestMismatch(t *testing.T) {
	t.Parallel()

	ref := refFor([]byte("declared"))
	err := mediafs.ValidateWrite(t.Context(), ref, []byte("actual"), "mediastore")
	if err == nil {
		t.Fatal("ValidateWrite accepted a payload that does not hash to ref.SHA256")
	}
	// The cache is content-addressed: a store that wrote these bytes under
	// this digest would hand every later reader an object failing its own
	// verification, so the refusal is the load-bearing behaviour here.
	if got := err.Error(); got != "mediastore: sha256 mismatch on write" {
		t.Fatalf("error = %q, want the prefixed mismatch message", got)
	}
}

func TestValidateWriteRejectsInvalidRef(t *testing.T) {
	t.Parallel()

	payload := []byte("payload")
	ref := refFor(payload)
	ref.Ext = "" // fails domain.MediaRef.Validate

	err := mediafs.ValidateWrite(t.Context(), ref, payload, "mediaadapter")
	if !errors.Is(err, domain.ErrMediaRef) {
		t.Fatalf("err = %v, want domain.ErrMediaRef", err)
	}
}

func TestValidateWriteHonoursCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	payload := []byte("payload")
	err := mediafs.ValidateWrite(ctx, refFor(payload), payload, "mediastore")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWriteObjectPersistsAtRefPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	payload := []byte("\x89PNG\r\n\x1a\nnot really a png but sniffable")
	ref := refFor(payload)

	obj, err := mediafs.WriteObject(root, "mediastore", ref, payload, "image/jpeg", 7, 1700000000)
	if err != nil {
		t.Fatalf("WriteObject: %v", err)
	}

	want := filepath.Join(root, ref.RelativePath())
	if obj.Path != want {
		t.Fatalf("Path = %q, want %q", obj.Path, want)
	}
	got, err := os.ReadFile(obj.Path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload round-trip mismatch")
	}

	// 0600 is the FR-042 file-permission floor for everything wa writes.
	info, err := os.Stat(obj.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %04o, want 0600", perm)
	}

	// The advertised mime is recorded verbatim (senders lie) while the
	// detected one is sniffed, so the two must not be conflated.
	if obj.MimeAdvertised != "image/jpeg" {
		t.Fatalf("MimeAdvertised = %q, want the caller's value", obj.MimeAdvertised)
	}
	if obj.MimeDetected == "" {
		t.Fatal("MimeDetected is empty; content sniffing did not run")
	}
	if obj.DurationSeconds != 7 || obj.FetchedAt != 1700000000 || obj.LastAccessAt != 1700000000 {
		t.Fatalf("metadata not carried through: %+v", obj)
	}
}

func TestWriteObjectLeavesNoTempFileBehind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	payload := []byte("payload")
	ref := refFor(payload)

	obj, err := mediafs.WriteObject(root, "mediastore", ref, payload, "application/octet-stream", 0, 1)
	if err != nil {
		t.Fatalf("WriteObject: %v", err)
	}

	// tmp+rename is only atomic if the temp file is gone afterwards;
	// a leaked ".tmp-*" would also be invisible to the sha-keyed GC sweep.
	entries, err := os.ReadDir(filepath.Dir(obj.Path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only the final object", names)
	}
}
