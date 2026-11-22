package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// png is a minimal PNG signature: http.DetectContentType sniffs image/png.
var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)

func mediaFixture(t *testing.T, hist *mediaHistory) (*MediaAdapter, *fakeWhatsmeowClient, string) {
	t.Helper()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.history = hist
	root := t.TempDir()
	m, err := a.NewMediaAdapter(root)
	if err != nil {
		t.Fatalf("NewMediaAdapter: %v", err)
	}
	return m, fc, root
}

func imageProto(t *testing.T, sha []byte, length uint64) []byte {
	t.Helper()
	raw, err := proto.Marshal(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		FileSHA256: sha, FileLength: proto.Uint64(length), Mimetype: proto.String("image/jpeg"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNewMediaAdapter_RejectsBadRoot(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, newFakeClient())
	t.Cleanup(func() { _ = a.Close() })
	if _, err := a.NewMediaAdapter("  "); err == nil {
		t.Error("blank root accepted")
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NewMediaAdapter(filepath.Join(file, "sub")); err == nil {
		t.Error("a root under a regular file was accepted")
	}
}

// TestDownload_FetchRejections: each permanent rejection names its cause and
// writes nothing to the cache. The advertised-size refusal must precede the
// network call.
func TestDownload_FetchRejections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	chat := domain.MustJID("5511999990001")
	good := sha256.Sum256(png)
	wrong := sha256.Sum256([]byte("other"))
	tests := []struct {
		name     string
		sha      []byte
		length   uint64
		payload  []byte
		dlErr    error
		wantIs   error
		wantText string
		wantDL   bool
	}{
		{"advertised over the cap", good[:], domain.MaxInboundMediaBytes + 1, png, nil, domain.ErrMessageTooLarge, "", false},
		{"payload over the cap", nil, 1, make([]byte, domain.MaxInboundMediaBytes+1), nil, domain.ErrMessageTooLarge, "mediaadapter: payload", true},
		{"download failure", good[:], uint64(len(png)), nil, errors.New("cdn 404"), nil, "download", true},
		{"sha mismatch", wrong[:], uint64(len(png)), png, nil, nil, "sha256 mismatch", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, fc, root := mediaFixture(t, &mediaHistory{chatJID: chat.String(), rawProto: imageProto(t, tt.sha, tt.length)})
			downloaded := false
			fc.DownloadAnyFn = func(context.Context, *waE2E.Message) ([]byte, error) {
				downloaded = true
				return tt.payload, tt.dlErr
			}
			_, err := m.Download(ctx, chat, "M1", false)
			if err == nil || (tt.wantIs != nil && !errors.Is(err, tt.wantIs)) || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("err = %v, want is=%v text=%q", err, tt.wantIs, tt.wantText)
			}
			if downloaded != tt.wantDL {
				t.Errorf("DownloadAny called = %v, want %v", downloaded, tt.wantDL)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Errorf("a rejected download left %d entries in the cache", len(entries))
			}
		})
	}
}

func TestDownload_RefusesEmptyIDAndCancelledContext(t *testing.T) {
	t.Parallel()
	m, _, _ := mediaFixture(t, &mediaHistory{})
	if _, err := m.Download(context.Background(), domain.MustJID("5511999990001"), "", false); err == nil {
		t.Error("empty message id accepted")
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Download(c, domain.JID{}, "M1", false); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// TestDownload_WritesSniffedObjectThenServesCache: a cold fetch stores the
// bytes under the verified digest with the sniffed type, a warm one is
// served from disk (Cached, zero bytes fetched), and Resolve finds it.
func TestDownload_WritesSniffedObjectThenServesCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	chat := domain.MustJID("5511999990001")
	sum := sha256.Sum256(png)
	m, fc, root := mediaFixture(t, &mediaHistory{chatJID: chat.String(), rawProto: imageProto(t, sum[:], uint64(len(png)))})
	calls := 0
	fc.DownloadAnyFn = func(context.Context, *waE2E.Message) ([]byte, error) { calls++; return png, nil }

	cold, err := m.Download(ctx, chat, "M1", false)
	if err != nil || cold.Cached || cold.BytesFetched != int64(len(png)) || cold.Object.MimeDetected != "image/png" || cold.Object.Ref.Ext != "png" {
		t.Fatalf("cold = %+v, %v", cold, err)
	}
	hexSum := hex.EncodeToString(sum[:])
	if want := filepath.Join(root, hexSum[:2], hexSum[2:]+".png"); cold.Object.Path != want {
		t.Errorf("path = %s, want %s", cold.Object.Path, want)
	}
	if st, err := os.Stat(cold.Object.Path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("stored file = %v, %v; want mode 0600", st, err)
	}
	warm, err := m.Download(ctx, chat, "M1", false)
	if err != nil || !warm.Cached || warm.BytesFetched != 0 || calls != 1 {
		t.Fatalf("warm = %+v, %v after %d downloads; want a cache hit", warm, err, calls)
	}
	if obj, err := m.Resolve(ctx, sum); err != nil || obj.Path != cold.Object.Path {
		t.Errorf("Resolve = %+v, %v", obj, err)
	}
	// Write of bytes already stored returns the stored object, not a second file.
	again, err := m.Write(ctx, cold.Object.Ref, png, "image/png", 0)
	if err != nil || again.Path != cold.Object.Path {
		t.Errorf("duplicate Write = %+v, %v", again, err)
	}
}

func TestResolve_MissAndCancel(t *testing.T) {
	t.Parallel()
	m, _, _ := mediaFixture(t, &mediaHistory{})
	if _, err := m.Resolve(context.Background(), sha256.Sum256([]byte("none"))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("miss: %v, want os.ErrNotExist", err)
	}
	if _, err := m.Resolve(context.Background(), [32]byte{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("zero sha: %v, want os.ErrNotExist", err)
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Resolve(c, [32]byte{1}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// TestGC_CutoffDryRunAndDelete: only files older than the cutoff are
// candidates; a dry run reports them (with digest and age) and deletes
// nothing; a real run deletes exactly them; in-flight .tmp- files and a
// missing root are left alone.
func TestGC_CutoffDryRunAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _, root := mediaFixture(t, &mediaHistory{})
	now := m.nowFn()
	put := func(sha [32]byte, name string, age time.Duration, body string) string {
		h := hex.EncodeToString(sha[:])
		dir := filepath.Join(root, h[:2])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, strings.ReplaceAll(name, "<h>", h[2:]))
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldSHA, newSHA := sha256.Sum256([]byte("old")), sha256.Sum256([]byte("new"))
	oldP := put(oldSHA, "<h>.png", 48*time.Hour, "old-bytes")
	newP := put(newSHA, "<h>.png", time.Hour, "new")
	tmpP := put(oldSHA, ".tmp-123", 72*time.Hour, "partial")
	cutoff := now.Add(-24 * time.Hour)

	dry, err := m.GC(ctx, cutoff, true)
	if err != nil || !dry.DryRun || dry.Candidates != 1 || dry.Deleted != 0 || dry.BytesFreed != int64(len("old-bytes")) || len(dry.Files) != 1 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if f := dry.Files[0]; f.SHA256 != hex.EncodeToString(oldSHA[:]) || f.Path != oldP || f.AgeSeconds != int64((48*time.Hour).Seconds()) {
		t.Errorf("candidate = %+v", f)
	}
	for _, p := range []string{oldP, newP, tmpP} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("dry run touched %s: %v", p, err)
		}
	}
	swept, err := m.GC(ctx, cutoff, false)
	if err != nil || swept.Deleted != 1 || swept.Candidates != 1 {
		t.Fatalf("real run = %+v, %v", swept, err)
	}
	if _, err := os.Stat(oldP); !errors.Is(err, os.ErrNotExist) {
		t.Error("the stale object survived GC")
	}
	for _, p := range []string{newP, tmpP} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("GC removed %s", p)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if r, err := m.GC(ctx, cutoff, false); err != nil || r.Candidates != 0 {
		t.Errorf("GC over a missing root = %+v, %v; want an empty report", r, err)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.GC(c, cutoff, true); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// TestInspectMedia_EdgeCases: the read path never errors for "no media".
// A cached object is reported with its path and size without its mtime
// being bumped (so listing media cannot keep it alive across GC).
func TestInspectMedia_EdgeCases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sum := sha256.Sum256(png)
	text, _ := proto.Marshal(&waE2E.Message{Conversation: proto.String("hi")})

	for name, hist := range map[string]*mediaHistory{
		"missing row": {err: os.ErrNotExist},
		"empty proto": {chatJID: "5511999990001@s.whatsapp.net"},
		"text only":   {chatJID: "5511999990001@s.whatsapp.net", rawProto: text},
	} {
		m, _, _ := mediaFixture(t, hist)
		info, present, err := m.InspectMedia(ctx, "", "M1")
		if err != nil || present || info.MessageID != "M1" || info.SHA256 != "" {
			t.Errorf("%s: = %+v present=%v err=%v; want absent, no error", name, info, present, err)
		}
	}
	m, _, _ := mediaFixture(t, &mediaHistory{err: errors.New("db locked")})
	if _, _, err := m.InspectMedia(ctx, "", "M1"); err == nil {
		t.Error("a history failure was swallowed")
	}
	m, _, _ = mediaFixture(t, &mediaHistory{rawProto: []byte{0xff, 0xff, 0xff}})
	if _, _, err := m.InspectMedia(ctx, "", "M1"); err == nil {
		t.Error("a corrupt proto was swallowed")
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := m.InspectMedia(c, "", "M1"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}

	m, _, _ = mediaFixture(t, &mediaHistory{chatJID: "5511999990001@s.whatsapp.net", rawProto: imageProto(t, sum[:], uint64(len(png)))})
	cold, present, err := m.InspectMedia(ctx, "", "M1")
	if err != nil || !present || cold.Cached || cold.Path != "" {
		t.Fatalf("uncached = %+v present=%v err=%v", cold, present, err)
	}
	obj, err := m.Write(ctx, domain.MediaRef{SHA256: sum, Mime: "image/png", Size: int64(len(png)), Ext: "png"}, png, "image/png", 0)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(obj.Path, past, past); err != nil {
		t.Fatal(err)
	}
	warm, _, err := m.InspectMedia(ctx, "", "M1")
	if err != nil || !warm.Cached || warm.Path != obj.Path || warm.CachedSize != int64(len(png)) {
		t.Fatalf("cached = %+v, %v", warm, err)
	}
	if st, _ := os.Stat(obj.Path); !st.ModTime().Before(time.Now().Add(-99 * time.Hour)) {
		t.Error("InspectMedia bumped the access time")
	}
}

func TestMediaHelpers(t *testing.T) {
	t.Parallel()
	sha := bytes.Repeat([]byte{7}, 32)
	for name, tt := range map[string]struct {
		msg      *waE2E.Message
		mime     string
		size     int64
		duration int64
	}{
		"image":    {&waE2E.Message{ImageMessage: &waE2E.ImageMessage{FileSHA256: sha, Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}}, "image/jpeg", 10, 0},
		"video":    {&waE2E.Message{VideoMessage: &waE2E.VideoMessage{FileSHA256: sha, Mimetype: proto.String("video/mp4"), FileLength: proto.Uint64(20), Seconds: proto.Uint32(9)}}, "video/mp4", 20, 9},
		"audio":    {&waE2E.Message{AudioMessage: &waE2E.AudioMessage{FileSHA256: sha, Mimetype: proto.String("audio/ogg"), FileLength: proto.Uint64(30), Seconds: proto.Uint32(4)}}, "audio/ogg", 30, 4},
		"document": {&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileSHA256: sha, Mimetype: proto.String("application/pdf"), FileLength: proto.Uint64(40)}}, "application/pdf", 40, 0},
		"sticker":  {&waE2E.Message{StickerMessage: &waE2E.StickerMessage{FileSHA256: sha, Mimetype: proto.String("image/webp"), FileLength: proto.Uint64(50)}}, "image/webp", 50, 0},
	} {
		got, mime, size, dur, ok := extractMediaHints(tt.msg)
		if !ok || got[0] != 7 || mime != tt.mime || size != tt.size || dur != tt.duration {
			t.Errorf("%s: hints = %x %q %d %d ok=%v", name, got[:2], mime, size, dur, ok)
		}
	}
	// A digest of the wrong length is ignored, not truncated or padded.
	short, _, _, _, ok := extractMediaHints(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{FileSHA256: []byte{1, 2, 3}}})
	if !ok || short != ([32]byte{}) {
		t.Errorf("short digest = %x ok=%v, want zero digest with media present", short[:4], ok)
	}

	h := hex.EncodeToString(sha)
	if got := shaFromPath(filepath.Join("root", h[:2], h[2:]+".jpg")); got != h {
		t.Errorf("shaFromPath = %q, want %q", got, h)
	}
	if got := shaFromPath(filepath.Join("root", "ab", "stray.txt")); got != "" {
		t.Errorf("shaFromPath of a stray file = %q, want empty", got)
	}
	if got := stripMimeParams("audio/ogg; codecs=opus"); got != "audio/ogg" {
		t.Errorf("stripMimeParams = %q", got)
	}
	if got := sniffMime([]byte{1, 2, 3, 4}, "audio/ogg"); got != "audio/ogg" {
		t.Errorf("sniffMime fell through to %q, want the advertised type", got)
	}
	if got := sniffMime([]byte{1, 2, 3, 4}, ""); got != "application/octet-stream" {
		t.Errorf("sniffMime = %q, want octet-stream", got)
	}
	if got := sniffMime(png, "image/jpeg"); got != "image/png" {
		t.Errorf("sniffMime = %q, want the sniffed type over the advertised one", got)
	}
	if got := sniffExt(png, ""); got != "png" {
		t.Errorf("sniffExt = %q", got)
	}
}
