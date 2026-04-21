package whatsmeow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// MediaAdapter satisfies app.MediaStore against the whatsmeow client and
// the local history store (raw_proto column). Storage is content-addressed
// on the filesystem; there is no in-memory index because the directory
// layout is itself the index.
//
// FR-050 requires Resolve to be pure (no network). Download is the only
// method that may hit whatsmeow. FR-051 requires Download to be idempotent:
// a cached payload returns Cached=true with BytesFetched=0.
type MediaAdapter struct {
	client  whatsmeowClient
	history historyContainer
	root    string
	nowFn   func() time.Time
}

// NewMediaAdapter returns a MediaAdapter rooted at root (created if absent).
// root is typically $XDG_CACHE_HOME/wa/media/sha256 shared across profiles.
func (a *Adapter) NewMediaAdapter(root string) (*MediaAdapter, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("mediaadapter: root is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mediaadapter: mkdir %s: %w", root, err)
	}
	nowFn := a.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	return &MediaAdapter{
		client:  a.client,
		history: a.history,
		root:    root,
		nowFn:   nowFn,
	}, nil
}

// Resolve implements app.MediaStore. FR-050: pure filesystem lookup.
func (m *MediaAdapter) Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error) {
	if err := ctx.Err(); err != nil {
		return domain.MediaObject{}, err
	}
	path, err := m.findBySHA(sha)
	if err != nil {
		return domain.MediaObject{}, err
	}
	return m.loadObject(path, sha, "", 0)
}

// Download implements app.MediaStore. Reconstructs the encrypted hints from
// raw_proto, tries the cache first, otherwise asks whatsmeow to decrypt.
// Linear pipeline — ctx check, proto load, hint extraction, cache lookup,
// size guard, network fetch, sha verify, atomic persist. Each branch is a
// distinct contract and is clearer inline than extracted.
//
//nolint:gocyclo // linear release-gated pipeline; extraction scatters audit
func (m *MediaAdapter) Download(ctx context.Context, messageID domain.MessageID, transcribe bool) (app.DownloadReport, error) {
	if err := ctx.Err(); err != nil {
		return app.DownloadReport{}, err
	}
	if messageID == "" {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: empty messageID")
	}

	_, rawProto, err := m.history.GetRawProto(ctx, string(messageID))
	if err != nil {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: lookup proto: %w", err)
	}
	if len(rawProto) == 0 {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: %s: %w", messageID, os.ErrNotExist)
	}

	msg := &waE2E.Message{}
	if err := proto.Unmarshal(rawProto, msg); err != nil {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: unmarshal proto: %w", err)
	}

	sha, advertisedMime, advertisedSize, duration, mediaPresent := extractMediaHints(msg)
	if !mediaPresent {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: %s has no media payload: %w", messageID, os.ErrNotExist)
	}

	if sha != ([32]byte{}) {
		if path, findErr := m.findBySHA(sha); findErr == nil {
			if obj, loadErr := m.loadObject(path, sha, advertisedMime, duration); loadErr == nil {
				_ = transcribe // transcription handled by a higher layer
				return app.DownloadReport{Object: obj, Cached: true, BytesFetched: 0}, nil
			}
		}
	}

	if advertisedSize > 0 && advertisedSize > domain.MaxInboundMediaBytes {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: advertised size %d exceeds %d: %w",
			advertisedSize, domain.MaxInboundMediaBytes, domain.ErrMessageTooLarge)
	}

	payload, err := m.client.DownloadAny(ctx, msg)
	if err != nil {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: download: %w", err)
	}
	if int64(len(payload)) > domain.MaxInboundMediaBytes {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: payload %d exceeds %d: %w",
			len(payload), domain.MaxInboundMediaBytes, domain.ErrMessageTooLarge)
	}

	gotSHA := sha256.Sum256(payload)
	if sha != ([32]byte{}) && gotSHA != sha {
		return app.DownloadReport{}, fmt.Errorf("mediaadapter: sha256 mismatch: want %x got %x", sha, gotSHA)
	}

	ref := domain.MediaRef{
		SHA256: gotSHA,
		Mime:   sniffMime(payload, advertisedMime),
		Size:   int64(len(payload)),
		Ext:    sniffExt(payload, advertisedMime),
	}
	obj, err := m.Write(ctx, ref, payload, advertisedMime, duration)
	if err != nil {
		return app.DownloadReport{}, err
	}
	return app.DownloadReport{Object: obj, Cached: false, BytesFetched: int64(len(payload))}, nil
}

// Write implements app.MediaStore. Atomic tmp+rename, 0600 perms.
func (m *MediaAdapter) Write(ctx context.Context, ref domain.MediaRef, payload []byte, advertisedMime string, duration int64) (domain.MediaObject, error) {
	if err := ctx.Err(); err != nil {
		return domain.MediaObject{}, err
	}
	if err := ref.Validate(); err != nil {
		return domain.MediaObject{}, err
	}
	if sha256.Sum256(payload) != ref.SHA256 {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: sha256 mismatch on write")
	}

	if existingPath, err := m.findBySHA(ref.SHA256); err == nil {
		return m.loadObject(existingPath, ref.SHA256, advertisedMime, duration)
	}

	full := filepath.Join(m.root, ref.RelativePath())
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".tmp-*")
	if err != nil {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, full); err != nil {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: rename: %w", err)
	}

	now := m.nowFn().Unix()
	detected := http.DetectContentType(payload)
	return domain.MediaObject{
		Ref:             ref,
		Path:            full,
		MimeAdvertised:  advertisedMime,
		MimeDetected:    detected,
		DurationSeconds: duration,
		FetchedAt:       now,
		LastAccessAt:    now,
	}, nil
}

// GC implements app.MediaStore. Walks the content-addressed tree and
// removes files whose mtime predates cutoff (mtime doubles as
// last-access-at, bumped by Resolve via os.Chtimes).
func (m *MediaAdapter) GC(ctx context.Context, cutoff time.Time, dryRun bool) (app.GCReport, error) {
	if err := ctx.Err(); err != nil {
		return app.GCReport{}, err
	}
	report := app.GCReport{DryRun: dryRun}
	walkErr := filepath.WalkDir(m.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // transient stat races are non-fatal for GC
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		report.Candidates++
		report.BytesFreed += info.Size()
		if dryRun {
			return nil
		}
		// The walk descends only paths under m.root, which we mkdir'd with
		// 0700 at construction time. We don't follow symlinks (WalkDir
		// default) and only Remove files whose path was produced by
		// WalkDir's own traversal of that private root. A TOCTOU attacker
		// would need write access to the 0700 dir, at which point they
		// already own the data.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) { //nolint:gosec // G122: path is walk-produced under 0700 root
			return fmt.Errorf("mediaadapter: remove %s: %w", path, rmErr)
		}
		report.Deleted++
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		return report, fmt.Errorf("mediaadapter: walk: %w", walkErr)
	}
	return report, nil
}

// findBySHA returns the on-disk path keyed by sha256. Returns a wrapped
// os.ErrNotExist on miss.
func (m *MediaAdapter) findBySHA(sha [32]byte) (string, error) {
	if sha == ([32]byte{}) {
		return "", fmt.Errorf("mediaadapter: zero sha256: %w", os.ErrNotExist)
	}
	h := hex.EncodeToString(sha[:])
	pattern := filepath.Join(m.root, h[:2], h[2:]+".*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("mediaadapter: glob: %w", err)
	}
	for _, p := range matches {
		if !strings.HasPrefix(filepath.Base(p), ".tmp-") {
			return p, nil
		}
	}
	return "", fmt.Errorf("mediaadapter: %x: %w", sha, os.ErrNotExist)
}

// loadObject builds a MediaObject from an on-disk file and bumps its
// access time so GC treats it as recently used.
func (m *MediaAdapter) loadObject(path string, sha [32]byte, advertisedMime string, duration int64) (domain.MediaObject, error) {
	info, err := os.Stat(path)
	if err != nil {
		return domain.MediaObject{}, fmt.Errorf("mediaadapter: stat: %w", err)
	}
	head, err := readHead(path)
	if err != nil {
		return domain.MediaObject{}, err
	}
	detected := http.DetectContentType(head)
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		ext = "bin"
	}
	ref := domain.MediaRef{
		SHA256: sha,
		Mime:   detected,
		Size:   info.Size(),
		Ext:    ext,
	}
	now := m.nowFn()
	_ = os.Chtimes(path, now, now)
	return domain.MediaObject{
		Ref:             ref,
		Path:            path,
		MimeAdvertised:  advertisedMime,
		MimeDetected:    detected,
		DurationSeconds: duration,
		FetchedAt:       info.ModTime().Unix(),
		LastAccessAt:    now.Unix(),
	}, nil
}

func readHead(path string) ([]byte, error) {
	// path is always produced by findBySHA (filepath.Glob under m.root) or
	// by WalkDir traversal of the same 0700 root. Callers never forward
	// attacker-controlled input here.
	f, err := os.Open(path) //nolint:gosec // G304: path is walk-produced under 0700 media root
	if err != nil {
		return nil, fmt.Errorf("mediaadapter: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("mediaadapter: read: %w", err)
	}
	return buf[:n], nil
}

// extractMediaHints pulls the downloadable fields off whichever media
// sub-message is present. mediaPresent=false means the message is
// text-only / reaction / etc.
func extractMediaHints(msg *waE2E.Message) (sha [32]byte, advertisedMime string, size int64, duration int64, mediaPresent bool) {
	if img := msg.GetImageMessage(); img != nil {
		copyShaBytes(&sha, img.GetFileSHA256())
		return sha, img.GetMimetype(), int64(img.GetFileLength()), 0, true //nolint:gosec
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		copyShaBytes(&sha, vid.GetFileSHA256())
		return sha, vid.GetMimetype(), int64(vid.GetFileLength()), int64(vid.GetSeconds()), true //nolint:gosec
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		copyShaBytes(&sha, aud.GetFileSHA256())
		return sha, aud.GetMimetype(), int64(aud.GetFileLength()), int64(aud.GetSeconds()), true //nolint:gosec
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		copyShaBytes(&sha, doc.GetFileSHA256())
		return sha, doc.GetMimetype(), int64(doc.GetFileLength()), 0, true //nolint:gosec
	}
	if stk := msg.GetStickerMessage(); stk != nil {
		copyShaBytes(&sha, stk.GetFileSHA256())
		return sha, stk.GetMimetype(), int64(stk.GetFileLength()), 0, true //nolint:gosec
	}
	return [32]byte{}, "", 0, 0, false
}

func copyShaBytes(dst *[32]byte, src []byte) {
	if len(src) != 32 {
		return
	}
	copy(dst[:], src)
}

func sniffMime(payload []byte, advertised string) string {
	detected := http.DetectContentType(payload)
	if detected != "" && !strings.HasPrefix(detected, "application/octet-stream") {
		return detected
	}
	if advertised != "" {
		return advertised
	}
	return "application/octet-stream"
}

func sniffExt(payload []byte, advertisedMime string) string {
	detected := http.DetectContentType(payload)
	if exts, _ := mime.ExtensionsByType(stripMimeParams(detected)); len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	if exts, _ := mime.ExtensionsByType(stripMimeParams(advertisedMime)); len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	return "bin"
}

func stripMimeParams(s string) string {
	if i := strings.Index(s, ";"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Compile-time assertion that MediaAdapter satisfies app.MediaStore.
var _ app.MediaStore = (*MediaAdapter)(nil)
