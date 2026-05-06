package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// MediaRoot is a content-addressed base directory for media blobs. The
// XDG layout per data-model.md is $XDG_CACHE_HOME/wa/media/sha256/. Stored
// absolute paths under Root are derived by joining MediaRef.RelativePath
// onto Root.
type MediaRoot struct {
	Root string
}

// NewMediaRoot validates that root is an absolute path and returns a
// MediaRoot rooted there.
func NewMediaRoot(root string) (MediaRoot, error) {
	if root == "" {
		return MediaRoot{}, errors.New("media: root path is empty")
	}
	if !filepath.IsAbs(root) {
		return MediaRoot{}, fmt.Errorf("media: root %q must be absolute", root)
	}
	clean := filepath.Clean(root)
	if strings.HasSuffix(clean, string(filepath.Separator)) {
		clean = strings.TrimRight(clean, string(filepath.Separator))
	}
	return MediaRoot{Root: clean}, nil
}

// AbsolutePath returns the deterministic on-disk path for ref. The path
// is NOT guaranteed to exist; callers must os.Stat before reading.
func (r MediaRoot) AbsolutePath(ref domain.MediaRef) string {
	return filepath.Join(r.Root, ref.RelativePath())
}

// DownloadSemaphore bounds concurrent MediaStore.Download invocations to
// the FR-050a ceiling (4 per daemon). Acquire blocks; Release is safe to
// call even on a cancelled path.
type DownloadSemaphore struct {
	slots chan struct{}
}

// DefaultDownloadConcurrency is the FR-050a ceiling.
const DefaultDownloadConcurrency = 4

// NewDownloadSemaphore returns a semaphore with `slots` capacity. Zero or
// negative falls back to DefaultDownloadConcurrency.
func NewDownloadSemaphore(slots int) *DownloadSemaphore {
	if slots <= 0 {
		slots = DefaultDownloadConcurrency
	}
	return &DownloadSemaphore{slots: make(chan struct{}, slots)}
}

// Acquire blocks until a slot is available or ctx is cancelled. Returns
// a release func that must be called on both success and failure paths.
// Callers should `defer release()` immediately after a successful acquire.
func (s *DownloadSemaphore) Acquire(stop <-chan struct{}) (release func(), err error) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-stop:
		return func() {}, errors.New("media: download acquire cancelled")
	}
}

// InFlight returns the number of currently-held slots. Useful for the
// status RPC; not race-free under contention.
func (s *DownloadSemaphore) InFlight() int { return len(s.slots) }

// Cap returns the configured concurrency ceiling.
func (s *DownloadSemaphore) Cap() int { return cap(s.slots) }
