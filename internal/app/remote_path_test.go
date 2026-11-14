package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// remotePathFixture is a dispatcher whose media root holds one object,
// plus a secret outside the root and a symlink inside it pointing out.
type remotePathFixture struct {
	d       *app.Dispatcher
	adapter *memory.Adapter
	drafts  *memory.DraftStore
	inDir   string
	secret  string
	link    string
	dotdot  string
	logs    *syncBuffer
}

// syncBuffer is a goroutine-safe log sink for the dispatcher's slog lines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newRemotePathFixture(t *testing.T, root string) remotePathFixture {
	t.Helper()
	base := t.TempDir()
	media := filepath.Join(base, "media", "sha256")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(media, "ab"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := remotePathFixture{
		inDir:  filepath.Join(media, "ab", "ab12"),
		secret: filepath.Join(outside, "tokens.db"),
		link:   filepath.Join(media, "ab", "escape"),
	}
	// Built by string, not Join, so the ".." survives to the dispatcher.
	f.dotdot = media + "/../../outside/tokens.db"
	for _, p := range []string{f.inDir, f.secret} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(f.secret, f.link); err != nil {
		t.Fatal(err)
	}
	if root == "media" {
		root = media
	}
	f.adapter = memory.New(nil)
	f.drafts = memory.NewDraftStore()
	f.logs = &syncBuffer{}
	f.d = app.NewDispatcher(app.DispatcherConfig{
		Sender: f.adapter, Events: f.adapter, Contacts: f.adapter, Groups: f.adapter,
		Session: f.adapter, Allowlist: f.adapter, Audit: f.adapter, History: f.adapter,
		Pairer: f.adapter, Quoted: f.adapter, Drafts: f.drafts,
		Scheduled: newFakeScheduledStore(), GroupAdmin: memory.NewGroupAdmin(),
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		RemotePathRoot: root, Profile: "default",
		Logger: slog.New(slog.NewJSONHandler(f.logs, nil)),
	})
	t.Cleanup(func() { _ = f.d.Close() })
	f.adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)
	return f
}

// isPathRefusal reports the confinement refusal specifically, not some
// other invalid_params from the same method.
func isPathRefusal(err error) bool {
	return errors.Is(err, app.ErrInvalidParams) && strings.Contains(err.Error(), "media directory")
}

func sendMediaPath(ctx context.Context, d *app.Dispatcher, path string) error {
	params, _ := json.Marshal(map[string]string{"to": testJIDStr, "path": path, "mime": "image/jpeg"})
	_, err := d.Handle(ctx, "sendMedia", params)
	return err
}

// TestRemotePath_SendMediaConfinedToMediaRoot pins WA-MCP-SEC F1: a network
// caller's sendMedia path must resolve (realpath) to a regular file under
// the media root. Every refusal is the same invalid_params, so the answer
// is not an existence oracle, and nothing reaches the sender.
func TestRemotePath_SendMediaConfinedToMediaRoot(t *testing.T) {
	f := newRemotePathFixture(t, "media")
	remote := app.WithRemoteCaller(context.Background(), app.TransportREST)
	for name, path := range map[string]string{
		"etc passwd":     "/etc/passwd",
		"symlink escape": f.link,
		"dotdot escape":  f.dotdot,
		"missing in dir": filepath.Join(filepath.Dir(f.inDir), "nope"),
		"the root dir":   filepath.Dir(filepath.Dir(f.inDir)),
		"a dir in root":  filepath.Dir(f.inDir),
		"relative":       "ab/ab12",
	} {
		if err := sendMediaPath(remote, f.d, path); !isPathRefusal(err) {
			t.Errorf("%s: err = %v, want ErrInvalidParams", name, err)
		}
	}
	if n := len(f.adapter.Sent()); n != 0 {
		t.Fatalf("refused paths reached the sender %d times", n)
	}

	if err := sendMediaPath(remote, f.d, f.inDir); err != nil {
		t.Fatalf("in-dir file: %v", err)
	}
	sent := f.adapter.Sent()
	want, _ := filepath.EvalSymlinks(f.inDir)
	if len(sent) != 1 || sent[0].(domain.MediaMessage).Path != want {
		t.Fatalf("in-dir file: sent %+v, want one MediaMessage at %s", sent, want)
	}
}

// TestRemotePath_LocalCallerUnchanged: the unix socket (no remote mark)
// keeps the same-euid behaviour, so an out-of-root path is forwarded as is.
func TestRemotePath_LocalCallerUnchanged(t *testing.T) {
	f := newRemotePathFixture(t, "media")
	if err := sendMediaPath(app.WithLocalCaller(context.Background()), f.d, f.secret); err != nil {
		t.Fatalf("local sendMedia: %v", err)
	}
	if sent := f.adapter.Sent(); len(sent) != 1 || sent[0].(domain.MediaMessage).Path != f.secret {
		t.Fatalf("local path not forwarded verbatim: %+v", sent)
	}
}

// TestRemotePath_UnmarkedContextIsConfined (WA-MCP-SEC L1): the mark is
// fail-closed. A context nobody marked (a new transport, a webhook, a job
// worker built from Background) is treated as remote; only an explicit
// local mark, which survives derived contexts, is trusted.
func TestRemotePath_UnmarkedContextIsConfined(t *testing.T) {
	f := newRemotePathFixture(t, "media")
	if err := sendMediaPath(context.Background(), f.d, f.secret); !isPathRefusal(err) {
		t.Fatalf("unmarked ctx: err = %v, want the confinement refusal", err)
	}
	if !app.IsRemoteCaller(context.Background()) || app.IsRemoteCaller(app.WithLocalCaller(context.Background())) {
		t.Fatal("IsRemoteCaller must be true unmarked and false only when marked local")
	}
	derived, cancel := context.WithTimeout(app.WithLocalCaller(context.Background()), time.Hour)
	defer cancel()
	if app.IsRemoteCaller(derived) {
		t.Fatal("the local mark must survive a derived (worker) context")
	}
	if !app.IsRemoteCaller(app.WithRemoteCaller(app.WithLocalCaller(context.Background()), app.TransportMCP)) {
		t.Fatal("a later remote mark must win over a local one")
	}
}

// TestRemotePath_NoRootFailsClosed: a daemon with no media root refuses
// every remote path, even one that exists.
func TestRemotePath_NoRootFailsClosed(t *testing.T) {
	f := newRemotePathFixture(t, "")
	if err := sendMediaPath(app.WithRemoteCaller(context.Background(), app.TransportREST), f.d, f.inDir); !isPathRefusal(err) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
}

// TestRemotePath_OtherPathMethodsConfined: draft.create (the MCP draft
// mode route), schedule.send and group.edit iconPath take the same guard,
// and a remote draft stores the resolved path.
func TestRemotePath_OtherPathMethodsConfined(t *testing.T) {
	f := newRemotePathFixture(t, "media")
	remote := app.WithRemoteCaller(context.Background(), app.TransportREST)
	for method, params := range map[string]map[string]any{
		"draft.create":  {"to": testJIDStr, "path": f.link},
		"schedule.send": {"to": testJIDStr, "kind": "send_media", "mediaPath": "/etc/passwd", "fireAt": time.Now().Add(time.Hour).Unix()},
		"group.edit":    {"group": "120363000000000000@g.us", "iconPath": f.dotdot},
	} {
		raw, _ := json.Marshal(params)
		if _, err := f.d.Handle(remote, method, raw); !isPathRefusal(err) {
			t.Errorf("%s: err = %v, want ErrInvalidParams", method, err)
		}
	}

	raw, _ := json.Marshal(map[string]any{"to": testJIDStr, "path": f.inDir})
	if _, err := f.d.Handle(remote, "draft.create", raw); err != nil {
		t.Fatalf("draft.create in-dir: %v", err)
	}
	drafts, err := f.drafts.List(context.Background(), "default", domain.DraftPendingReview, 10)
	want, _ := filepath.EvalSymlinks(f.inDir)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %v, err %v", drafts, err)
	}
	var payload struct{ Path string }
	if err := json.Unmarshal([]byte(drafts[0].PayloadJSON), &payload); err != nil || payload.Path != want {
		t.Fatalf("draft payload path = %q (err %v), want %q", payload.Path, err, want)
	}
}

// TestRemotePath_RefusalLogsMethodAndTransportNotPath makes canary refusals
// countable: each refusal writes exactly one Warn line naming the method
// and the transport, and the caller-supplied path never reaches the log.
// An accepted path logs nothing.
func TestRemotePath_RefusalLogsMethodAndTransportNotPath(t *testing.T) {
	f := newRemotePathFixture(t, "media")
	mcp := app.WithRemoteCaller(context.Background(), app.TransportMCP)
	refused := []string{"/etc/passwd", f.secret, f.link, f.dotdot}
	for _, p := range refused {
		if err := sendMediaPath(mcp, f.d, p); !isPathRefusal(err) {
			t.Fatalf("%s: err = %v, want the path refusal", p, err)
		}
	}
	raw, _ := json.Marshal(map[string]string{"to": testJIDStr, "path": f.secret, "mime": "image/jpeg"})
	if _, err := f.d.Handle(app.WithRemoteCaller(context.Background(), app.TransportREST), "draft.create", raw); !isPathRefusal(err) {
		t.Fatalf("draft.create over rest: err = %v, want the path refusal", err)
	}
	if err := sendMediaPath(mcp, f.d, f.inDir); err != nil {
		t.Fatalf("in-dir file: %v", err)
	}

	logs := f.logs.String()
	for _, p := range append(refused, f.inDir, "outside", "tokens.db") {
		if strings.Contains(logs, p) {
			t.Fatalf("log contains path fragment %q:\n%s", p, logs)
		}
	}
	var got []string
	for line := range strings.Lines(logs) {
		var rec struct{ Level, Msg, Method, Transport string }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if rec.Msg != "remote media path refused" {
			continue
		}
		if rec.Level != "WARN" {
			t.Errorf("refusal logged at %s, want WARN", rec.Level)
		}
		got = append(got, rec.Method+"/"+rec.Transport)
	}
	want := []string{"sendMedia/mcp", "sendMedia/mcp", "sendMedia/mcp", "sendMedia/mcp", "draft.create/rest"}
	if !slices.Equal(got, want) {
		t.Fatalf("refusal lines = %v, want %v", got, want)
	}
}
