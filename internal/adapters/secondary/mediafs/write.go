// Package mediafs holds the on-disk mechanics shared by the app.MediaStore
// implementations that persist into the content-addressed media cache.
//
// The whatsmeow adapter and the memory store both write the same
// sha256-keyed layout and each carried its own byte-identical copy of the
// tmp+rename sequence. Two copies of a durability-critical write path drift
// silently — an fsync or a permission tightening applied to one leaves the
// other unpatched, and the memory store is what the port-contract suite
// exercises, so the untouched copy is the one under test.
package mediafs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ValidateWrite runs the checks every media write shares before a byte
// reaches the disk: cancellation, ref well-formedness, and payload-versus-
// digest agreement.
//
// The digest check is the load-bearing one. The cache is content-addressed,
// so a store that wrote a payload under a sha256 it does not hash to would
// hand every later reader — including a different store sharing the same
// root — bytes that fail their own verification. Callers run this before
// their dedup probe so an invalid ref cannot be answered from cache.
func ValidateWrite(ctx context.Context, ref domain.MediaRef, payload []byte, errPrefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if sha256.Sum256(payload) != ref.SHA256 {
		return fmt.Errorf("%s: sha256 mismatch on write", errPrefix)
	}
	return nil
}

// WriteObject persists payload at root/ref.RelativePath() and returns the
// resulting object.
//
// The write is atomic: payload lands in a temp file inside the DESTINATION
// directory — same filesystem, so the rename cannot cross a mount boundary
// and degrade into a copy — is chmod'd 0600 before being linked into place,
// and only then renamed over the final path. A concurrent reader therefore
// sees either no file or the whole object, never a truncated one. The temp
// file is removed on every failure path.
//
// It deliberately does NOT decide whether the object already exists: dedup
// is the caller's policy (a map lookup in the memory store, a directory
// probe in the whatsmeow adapter) and the two disagree on purpose.
//
// errPrefix names the calling store in returned errors ("mediaadapter",
// "mediastore") so a failure still says which one produced it.
func WriteObject(
	root, errPrefix string,
	ref domain.MediaRef,
	payload []byte,
	advertisedMime string,
	duration, now int64,
) (domain.MediaObject, error) {
	full := filepath.Join(root, ref.RelativePath())
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return domain.MediaObject{}, fmt.Errorf("%s: mkdir: %w", errPrefix, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".tmp-*")
	if err != nil {
		return domain.MediaObject{}, fmt.Errorf("%s: tmp: %w", errPrefix, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return domain.MediaObject{}, fmt.Errorf("%s: write tmp: %w", errPrefix, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return domain.MediaObject{}, fmt.Errorf("%s: chmod tmp: %w", errPrefix, err)
	}
	if err := tmp.Close(); err != nil {
		return domain.MediaObject{}, fmt.Errorf("%s: close tmp: %w", errPrefix, err)
	}
	if err := os.Rename(tmpPath, full); err != nil {
		return domain.MediaObject{}, fmt.Errorf("%s: rename: %w", errPrefix, err)
	}

	return domain.MediaObject{
		Ref:             ref,
		Path:            full,
		MimeAdvertised:  advertisedMime,
		MimeDetected:    http.DetectContentType(payload),
		DurationSeconds: duration,
		FetchedAt:       now,
		LastAccessAt:    now,
	}, nil
}
