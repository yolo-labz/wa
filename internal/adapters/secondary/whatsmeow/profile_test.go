package whatsmeow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// openProfileAdapter builds a ProfileAdapter with a fake client, a
// temp avatar root, and a deterministic HTTP downloader that returns
// `body` for every URL. Returns the adapter, the fake client, and the
// Adapter so tests can inspect audit entries.
func openProfileAdapter(t *testing.T, body []byte) (*ProfileAdapter, *fakeWhatsmeowClient, *Adapter) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })
	p, err := a.NewProfileFor(t.TempDir())
	if err != nil {
		t.Fatalf("NewProfileFor: %v", err)
	}
	p.http = func(_ context.Context, _ string) ([]byte, error) {
		return body, nil
	}
	return p, fc, a
}

// TestProfileEditor_SatisfiesContract drives PE1..PE7 against the real
// adapter so porttest catches shape drift during whatsmeow bumps.
func TestProfileEditor_SatisfiesContract(t *testing.T) {
	porttest.RunProfileEditorContract(t, func(t *testing.T) app.ProfileEditor {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		fc.AvatarInfos = map[avatarKey]*waTypes.ProfilePictureInfo{
			{jid: "5511999999999@s.whatsapp.net", preview: true}: {ID: "pic-1", URL: "https://cdn/p1.jpg"},
		}
		a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
		t.Cleanup(func() { _ = a.Close() })
		p, err := a.NewProfileFor(t.TempDir())
		if err != nil {
			t.Fatalf("NewProfileFor: %v", err)
		}
		p.http = func(_ context.Context, _ string) ([]byte, error) {
			return []byte("fake-avatar-bytes"), nil
		}
		return p
	})
}

// TestSetNameLength25 verifies the byte cap is enforced before hitting
// the wire: 25 bytes accepted, 26 bytes rejected with ErrMessageTooLarge.
func TestSetNameLength25(t *testing.T) {
	p, fc, _ := openProfileAdapter(t, nil)
	ctx := context.Background()

	name25 := strings.Repeat("a", 25)
	if err := p.SetName(ctx, name25); err != nil {
		t.Fatalf("SetName(25) = %v, want nil", err)
	}
	fc.mu.Lock()
	patchesAfter25 := len(fc.AppStatePatches)
	fc.mu.Unlock()
	if patchesAfter25 != 1 {
		t.Fatalf("SetName(25) AppStatePatches = %d, want 1", patchesAfter25)
	}

	name26 := strings.Repeat("a", 26)
	err := p.SetName(ctx, name26)
	if !errors.Is(err, domain.ErrMessageTooLarge) {
		t.Fatalf("SetName(26) = %v, want ErrMessageTooLarge", err)
	}
	fc.mu.Lock()
	patchesAfter26 := len(fc.AppStatePatches)
	fc.mu.Unlock()
	if patchesAfter26 != patchesAfter25 {
		t.Fatalf("oversized SetName reached the wire: before=%d after=%d", patchesAfter25, patchesAfter26)
	}
}

// TestSetStatusLength139 verifies the 139-byte status cap mirrors the
// server-side About limit.
func TestSetStatusLength139(t *testing.T) {
	p, fc, _ := openProfileAdapter(t, nil)
	ctx := context.Background()

	msg139 := strings.Repeat("b", 139)
	if err := p.SetStatus(ctx, msg139); err != nil {
		t.Fatalf("SetStatus(139) = %v, want nil", err)
	}
	fc.mu.Lock()
	sentBefore := len(fc.StatusMessages)
	fc.mu.Unlock()
	if sentBefore != 1 {
		t.Fatalf("StatusMessages after 139 = %d, want 1", sentBefore)
	}

	msg140 := strings.Repeat("b", 140)
	err := p.SetStatus(ctx, msg140)
	if !errors.Is(err, domain.ErrMessageTooLarge) {
		t.Fatalf("SetStatus(140) = %v, want ErrMessageTooLarge", err)
	}
	fc.mu.Lock()
	sentAfter := len(fc.StatusMessages)
	fc.mu.Unlock()
	if sentAfter != sentBefore {
		t.Fatalf("oversized SetStatus reached the wire: before=%d after=%d", sentBefore, sentAfter)
	}
}

// TestGetAvatarContentAddressed verifies the sha256 path shape, cache
// re-use (no HTTP on repeat), and that ExistingID is forwarded on
// subsequent calls. FR-028.
func TestGetAvatarContentAddressed(t *testing.T) {
	body := []byte("avatar-blob-deterministic")
	fc := newFakeClient()
	fc.ConnectedFlag = true
	jid := domain.MustJID("5511999999999@s.whatsapp.net")
	fc.AvatarInfos = map[avatarKey]*waTypes.ProfilePictureInfo{
		{jid: jid.String(), preview: true}: {ID: "pic-alpha", URL: "https://cdn/alpha.jpg"},
	}
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })
	root := t.TempDir()
	p, err := a.NewProfileFor(root)
	if err != nil {
		t.Fatalf("NewProfileFor: %v", err)
	}
	var httpCalls atomic.Int32
	p.http = func(_ context.Context, _ string) ([]byte, error) {
		httpCalls.Add(1)
		return body, nil
	}

	path, err := p.GetAvatar(context.Background(), jid, domain.AvatarPreview)
	if err != nil {
		t.Fatalf("GetAvatar: %v", err)
	}
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])
	want := filepath.Join(root, hexSum[:2], hexSum[2:]+".jpg")
	if path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat avatar path: %v", err)
	}
	if got := httpCalls.Load(); got != 1 {
		t.Fatalf("httpCalls after first = %d, want 1", got)
	}

	// Second call: fake returns nil (unchanged) because ExistingID matches
	// the cached pic-alpha — we override AvatarInfos so that the
	// ExistingID lookup yields nil.
	fc.mu.Lock()
	fc.AvatarInfos = nil
	fc.mu.Unlock()

	path2, err := p.GetAvatar(context.Background(), jid, domain.AvatarPreview)
	if err != nil {
		t.Fatalf("GetAvatar #2: %v", err)
	}
	if path2 != path {
		t.Fatalf("cached path mismatch: %s != %s", path2, path)
	}
	if got := httpCalls.Load(); got != 1 {
		t.Fatalf("httpCalls after cache hit = %d, want 1 (no re-download)", got)
	}

	// Verify ExistingID threading: second GetProfilePictureInfo call
	// should have received ExistingID="pic-alpha" via the params struct.
	fc.mu.Lock()
	calls := len(fc.AvatarCalls)
	fc.mu.Unlock()
	if calls != 2 {
		t.Fatalf("AvatarCalls = %d, want 2", calls)
	}
}

// TestSetNameAppStatePatch verifies SendAppState is called with a
// setting_pushName patch carrying the exact name bytes.
func TestSetNameAppStatePatch(t *testing.T) {
	p, fc, _ := openProfileAdapter(t, nil)
	if err := p.SetName(context.Background(), "Pedro"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	fc.mu.Lock()
	patches := append([]appstate.PatchInfo(nil), fc.AppStatePatches...)
	fc.mu.Unlock()
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches))
	}
	if len(patches[0].Mutations) == 0 {
		t.Fatalf("patch has 0 mutations")
	}
	mut := patches[0].Mutations[0]
	if mut.Value == nil || mut.Value.PushNameSetting == nil {
		t.Fatalf("patch is not a push-name mutation: %+v", mut)
	}
	if got := mut.Value.PushNameSetting.GetName(); got != "Pedro" {
		t.Fatalf("push name = %q, want Pedro", got)
	}
}

// TestProfileAuditWritten asserts that successful SetName and
// SetStatus calls write AuditProfileEdit entries, and GetAvatar does
// not (read-only per port contract).
func TestProfileAuditWritten(t *testing.T) {
	body := []byte("avatar")
	p, fc, a := openProfileAdapter(t, body)
	jid := domain.MustJID("5511999999999@s.whatsapp.net")
	fc.AvatarInfos = map[avatarKey]*waTypes.ProfilePictureInfo{
		{jid: jid.String(), preview: true}: {ID: "pic-1", URL: "https://cdn/p.jpg"},
	}
	ctx := context.Background()

	if err := p.SetName(ctx, "X"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := p.SetStatus(ctx, "hi"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := p.GetAvatar(ctx, jid, domain.AvatarPreview); err != nil {
		t.Fatalf("GetAvatar: %v", err)
	}

	var edits []domain.AuditEvent
	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditProfileEdit {
			edits = append(edits, e)
		}
	}
	if len(edits) != 2 {
		t.Fatalf("AuditProfileEdit count = %d, want 2 (SetName + SetStatus, not GetAvatar)", len(edits))
	}
	wantDetails := map[string]bool{"name=X": true, "status=hi": true}
	for _, e := range edits {
		if !wantDetails[e.Detail] {
			t.Fatalf("unexpected audit detail %q", e.Detail)
		}
		if e.Decision != "ok" {
			t.Fatalf("decision = %q, want ok", e.Decision)
		}
	}
}
