package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/mediafs"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// MediaStore is the in-memory (tmp-dir backed) implementation of
// app.MediaStore per FR-050..FR-053.
type MediaStore struct {
	root    string
	mu      sync.Mutex
	objects map[[32]byte]domain.MediaObject
	byMsg   map[domain.MessageID][32]byte
	clock   Clock
}

// NewMediaStore returns a MediaStore rooted at root (created if absent).
// clk defaults to RealClock.
func NewMediaStore(root string, clk Clock) (*MediaStore, error) {
	if clk == nil {
		clk = RealClock{}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mediastore: mkdir %s: %w", root, err)
	}
	return &MediaStore{
		root:    root,
		objects: make(map[[32]byte]domain.MediaObject),
		byMsg:   make(map[domain.MessageID][32]byte),
		clock:   clk,
	}, nil
}

// SeedMessageMedia associates messageID with an already-persisted ref so
// Download() can resolve it. Test-only.
func (m *MediaStore) SeedMessageMedia(messageID domain.MessageID, sha [32]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byMsg[messageID] = sha
}

// Resolve implements app.MediaStore. FR-050: pure lookup, no network.
func (m *MediaStore) Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error) {
	if err := ctx.Err(); err != nil {
		return domain.MediaObject{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[sha]
	if !ok {
		return domain.MediaObject{}, fmt.Errorf("mediastore: %w", os.ErrNotExist)
	}
	obj.LastAccessAt = m.clock.Now().Unix()
	m.objects[sha] = obj
	return obj, nil
}

// Download implements app.MediaStore. The memory adapter has no remote
// source; if the message was seeded via SeedMessageMedia and the object
// is already cached, return Cached=true. Otherwise return
// domain.ErrMediaNotCached so higher layers surface -32301 MediaNotCached
// (matches the whatsmeow adapter contract — issue #102).
func (m *MediaStore) Download(ctx context.Context, messageID domain.MessageID, transcribe bool) (app.DownloadReport, error) {
	if err := ctx.Err(); err != nil {
		return app.DownloadReport{}, err
	}
	m.mu.Lock()
	sha, known := m.byMsg[messageID]
	if !known {
		m.mu.Unlock()
		return app.DownloadReport{}, fmt.Errorf("mediastore: %s: %w", messageID, domain.ErrMediaNotCached)
	}
	obj, cached := m.objects[sha]
	m.mu.Unlock()
	if !cached {
		return app.DownloadReport{}, fmt.Errorf("mediastore: %s: %w", messageID, domain.ErrMediaNotCached)
	}
	_ = transcribe
	return app.DownloadReport{Object: obj, Cached: true, BytesFetched: 0}, nil
}

// Write implements app.MediaStore. Writes are atomic (tmp + rename).
// Repeated calls for the same sha256 return the existing object unchanged
// (idempotent per MDS2).
func (m *MediaStore) Write(ctx context.Context, ref domain.MediaRef, payload []byte, advertisedMime string, duration int64) (domain.MediaObject, error) {
	if err := mediafs.ValidateWrite(ctx, ref, payload, "mediastore"); err != nil {
		return domain.MediaObject{}, err
	}

	// Dedup here is the objects map, not a directory probe: this store is
	// process-scoped, so a file left behind by an earlier run is not a hit.
	m.mu.Lock()
	if existing, ok := m.objects[ref.SHA256]; ok {
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	obj, err := mediafs.WriteObject(m.root, "mediastore", ref, payload, advertisedMime, duration, m.clock.Now().Unix())
	if err != nil {
		return domain.MediaObject{}, err
	}

	m.mu.Lock()
	m.objects[ref.SHA256] = obj
	m.mu.Unlock()
	return obj, nil
}

// GC implements app.MediaStore. Candidates are objects whose LastAccessAt
// predates cutoff. DryRun=true performs no deletions.
//
// This sweep is source-agnostic: media uploaded for send via
// POST /media/upload (#199) lands in the same sha-keyed objects map as
// fetched/downloaded media (both flow through Write), so an unreferenced
// uploaded object whose LastAccessAt ages past cutoff is reclaimed here
// exactly like any other cache entry — no separate path or exemption.
func (m *MediaStore) GC(ctx context.Context, cutoff time.Time, dryRun bool) (app.GCReport, error) {
	if err := ctx.Err(); err != nil {
		return app.GCReport{}, err
	}
	cutoffUnix := cutoff.Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	report := app.GCReport{DryRun: dryRun}
	for sha, obj := range m.objects {
		if obj.LastAccessAt >= cutoffUnix {
			continue
		}
		report.Candidates++
		report.BytesFreed += obj.Ref.Size
		if dryRun {
			continue
		}
		if err := os.Remove(obj.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, fmt.Errorf("mediastore: remove %s: %w", obj.Path, err)
		}
		delete(m.objects, sha)
		report.Deleted++
	}
	return report, nil
}
