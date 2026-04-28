package whatsmeow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Profile identity-field limits mirror WhatsApp's server-side caps.
// These constants are duplicated from the memory fake on purpose: the
// domain layer has no profile-edit type yet (FR-026 keeps it at the
// adapter boundary) and both adapters share the same wire contract.
const (
	maxProfileNameBytes   = 25
	maxProfileStatusBytes = 139
)

// ProfileAdapter is the whatsmeow-backed implementation of
// app.ProfileEditor (FR-026, FR-027, FR-028). SetName pushes a
// setting_pushName appstate patch; SetStatus uses the direct
// `status` IQ via Client.SetStatusMessage. GetAvatar fetches the CDN
// URL from the `w:profile:picture` IQ, downloads the bytes, and writes
// them content-addressed under
// $XDG_CACHE_HOME/wa/media/avatars/sha256/<aa>/<rest>.jpg. Repeated
// calls for the same (JID, size) hit the cache — the adapter keeps an
// in-memory (JID, size) -> (serverID, path) map and asks
// GetProfilePictureInfo with ExistingID on every subsequent call so the
// server can reply "no change" without transferring bytes.
type ProfileAdapter struct {
	client whatsmeowClient
	audit  *auditRingBuffer
	logger *slog.Logger
	nowFn  func() time.Time
	root   string
	http   avatarDownloader

	mu    sync.Mutex
	cache map[avatarCacheKey]avatarCacheEntry
}

// avatarDownloader isolates the HTTP GET for tests. The production
// value is httpAvatarFetcher (net/http), tests inject a stub.
type avatarDownloader func(ctx context.Context, url string) ([]byte, error)

type avatarCacheKey struct {
	jid  domain.JID
	size domain.AvatarSize
}

type avatarCacheEntry struct {
	id   string
	path string
}

// NewProfileFor wires a ProfileAdapter. root is typically
// PathResolver.AvatarRoot() — the shared cache directory for all
// profiles. Created with 0700 if missing.
func (a *Adapter) NewProfileFor(root string) (*ProfileAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewProfileFor: nil adapter/client")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("whatsmeow.NewProfileFor: empty avatar cache root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("whatsmeow.NewProfileFor: mkdir %s: %w", root, err)
	}
	nowFn := a.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	return &ProfileAdapter{
		client: a.client,
		audit:  a.auditBuf,
		logger: a.logger,
		nowFn:  nowFn,
		root:   root,
		http:   httpAvatarFetcher,
		cache:  make(map[avatarCacheKey]avatarCacheEntry),
	}, nil
}

// SetName enforces the 25-byte cap before touching the wire, then
// pushes a setting_pushName appstate mutation. Writes
// AuditProfileEdit on success.
func (p *ProfileAdapter) SetName(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(name) > maxProfileNameBytes {
		return fmt.Errorf("ProfileAdapter.SetName: %w: profile name %d > %d bytes",
			domain.ErrMessageTooLarge, len(name), maxProfileNameBytes)
	}
	if !p.client.IsConnected() {
		return fmt.Errorf("ProfileAdapter.SetName: %w", domain.ErrDisconnected)
	}
	if err := p.client.SendAppState(ctx, appstate.BuildSettingPushName(name)); err != nil {
		return fmt.Errorf("ProfileAdapter.SetName: %w", err)
	}
	p.recordAudit("name", name)
	return nil
}

// SetStatus enforces the 139-byte cap, then sends the `status` IQ.
// Writes AuditProfileEdit on success.
func (p *ProfileAdapter) SetStatus(ctx context.Context, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(status) > maxProfileStatusBytes {
		return fmt.Errorf("ProfileAdapter.SetStatus: %w: profile status %d > %d bytes",
			domain.ErrMessageTooLarge, len(status), maxProfileStatusBytes)
	}
	if !p.client.IsConnected() {
		return fmt.Errorf("ProfileAdapter.SetStatus: %w", domain.ErrDisconnected)
	}
	if err := p.client.SetStatusMessage(ctx, status); err != nil {
		return fmt.Errorf("ProfileAdapter.SetStatus: %w", err)
	}
	p.recordAudit("status", status)
	return nil
}

// GetAvatar returns the filesystem path of a content-addressed avatar.
// FR-028: path shape is
// $XDG_CACHE_HOME/wa/media/avatars/sha256/<aa>/<rest>.jpg. Repeated
// calls for the same (JID, size) return the cached path without
// HTTP I/O until the server reports a new picture ID.
func (p *ProfileAdapter) GetAvatar(ctx context.Context, jid domain.JID, size domain.AvatarSize) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if jid.IsZero() {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w", domain.ErrInvalidJID)
	}
	if !size.IsValid() {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: invalid size %d", uint8(size))
	}
	if !p.client.IsConnected() {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w", domain.ErrDisconnected)
	}

	key := avatarCacheKey{jid: jid, size: size}
	p.mu.Lock()
	entry, hit := p.cache[key]
	p.mu.Unlock()

	params := &whatsmeow.GetProfilePictureParams{
		Preview:    size == domain.AvatarPreview,
		ExistingID: entry.id,
	}
	info, err := p.client.GetProfilePictureInfo(ctx, toWhatsmeow(jid), params)
	if err != nil {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w", err)
	}
	// info==nil means "picture unchanged since ExistingID" — reuse cache.
	if info == nil {
		if hit {
			return entry.path, nil
		}
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w: no avatar set", domain.ErrUpstreamError)
	}

	bytes, err := p.http(ctx, info.URL)
	if err != nil {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: download %s: %w", info.URL, err)
	}
	if len(bytes) == 0 {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w: empty avatar body", domain.ErrUpstreamError)
	}
	path, err := p.writeContentAddressed(bytes)
	if err != nil {
		return "", fmt.Errorf("ProfileAdapter.GetAvatar: %w", err)
	}
	p.mu.Lock()
	p.cache[key] = avatarCacheEntry{id: info.ID, path: path}
	p.mu.Unlock()
	return path, nil
}

// writeContentAddressed computes sha256(bytes), maps it to the
// fan-out path <root>/<aa>/<rest>.jpg, and writes the file atomically
// (temp file + rename). Idempotent — re-writing the same hash is a
// no-op that still returns the path.
func (p *ProfileAdapter) writeContentAddressed(bytes []byte) (string, error) {
	sum := sha256.Sum256(bytes)
	hexSum := hex.EncodeToString(sum[:])
	dir := filepath.Join(p.root, hexSum[:2])
	path := filepath.Join(dir, hexSum[2:]+".jpg")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".avatar-*.tmp")
	if err != nil {
		return "", fmt.Errorf("createtemp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("rename: %w", err)
	}
	return path, nil
}

func (p *ProfileAdapter) recordAudit(field, value string) {
	if p.audit == nil {
		return
	}
	_ = p.audit.Record(context.Background(), domain.AuditEvent{
		TS:       p.nowFn(),
		Actor:    "whatsmeow",
		Action:   domain.AuditProfileEdit,
		Subject:  domain.JID{},
		Decision: "ok",
		Detail:   field + "=" + value,
	})
}

// httpAvatarFetcher is the production avatarDownloader. 10s timeout —
// profile photos are small (<200 KiB) and the CDN is fast. A slower
// response usually means "transient failure" and retrying on the next
// GetAvatar call is safe.
func httpAvatarFetcher(ctx context.Context, url string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}
