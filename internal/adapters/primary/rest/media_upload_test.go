package rest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeMediaWriter is a MediaWriter + MediaResolver double for the
// POST /media/upload handler tests. It content-addresses payloads in a temp
// dir so an uploaded object is also retrievable via the GET /media/{sha256}
// route, letting one test assert the round-trip end to end.
type fakeMediaWriter struct {
	t    *testing.T
	mu   sync.Mutex
	root string
	objs map[[32]byte]domain.MediaObject
	err  error // when non-nil, Write returns it (forces the 500 path)
}

func newFakeMediaWriter(t *testing.T) *fakeMediaWriter {
	t.Helper()
	return &fakeMediaWriter{t: t, root: t.TempDir(), objs: make(map[[32]byte]domain.MediaObject)}
}

func (f *fakeMediaWriter) Write(_ context.Context, ref domain.MediaRef, payload []byte, advertised string, dur int64) (domain.MediaObject, error) {
	if f.err != nil {
		return domain.MediaObject{}, f.err
	}
	if err := ref.Validate(); err != nil {
		return domain.MediaObject{}, err
	}
	if sha256.Sum256(payload) != ref.SHA256 {
		f.t.Fatalf("fakeMediaWriter: sha mismatch on write")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.objs[ref.SHA256]; ok {
		return existing, nil
	}
	path := filepath.Join(f.root, ref.HexSHA256()+"."+ref.Ext)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return domain.MediaObject{}, err
	}
	obj := domain.MediaObject{
		Ref:            ref,
		Path:           path,
		MimeAdvertised: advertised,
		MimeDetected:   ref.Mime,
		FetchedAt:      time.Now().Unix(),
	}
	f.objs[ref.SHA256] = obj
	return obj, nil
}

func (f *fakeMediaWriter) Resolve(_ context.Context, sha [32]byte) (domain.MediaObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objs[sha]
	if !ok {
		return domain.MediaObject{}, os.ErrNotExist
	}
	return obj, nil
}

// newUploadServer wires an env-token server with the upload (and matching
// fetch) route. mw==nil disables the upload route to exercise the 503 path.
func newUploadServer(t *testing.T, token string, mw *fakeMediaWriter) *Server {
	t.Helper()
	opts := []ServerOption{WithLogger(discardLogger())}
	if mw != nil {
		opts = append(opts, WithMediaUploader(mw), WithMediaStore(mw))
	}
	srv, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, NewEnvTokenAuth(token), opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

func postUpload(t *testing.T, addr, token string, body []byte) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/media/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// uploadResultOf decodes the JSON success body.
func uploadResultOf(t *testing.T, resp *http.Response) (schema, sha string, size int64) {
	t.Helper()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Schema string `json:"schema"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal upload result %q: %v", string(raw), err)
	}
	return out.Schema, out.SHA256, out.Size
}

func TestMediaUpload_OK(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)
	payload := []byte("hello upload payload bytes")
	want := sha256.Sum256(payload)

	resp := postUpload(t, srv.ListenerAddr().String(), "test-token", payload) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	schema, sha, size := uploadResultOf(t, resp)
	if schema != "wa.media.upload/v1" {
		t.Errorf("schema = %q, want wa.media.upload/v1", schema)
	}
	if sha != hex.EncodeToString(want[:]) {
		t.Errorf("sha256 = %q, want %q", sha, hex.EncodeToString(want[:]))
	}
	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
}

// TestMediaUpload_RoundTripsToFetch pins the security-relevant invariant:
// the sha returned by upload is immediately retrievable via the existing
// GET /media/{sha256} route, and the bytes match.
func TestMediaUpload_RoundTripsToFetch(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)
	payload := []byte("round-trip me through the content store")

	up := postUpload(t, srv.ListenerAddr().String(), "test-token", payload) //nolint:bodyclose // closed via t.Cleanup
	if up.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200", up.StatusCode)
	}
	_, sha, _ := uploadResultOf(t, up)

	got := getMedia(t, srv.ListenerAddr().String(), "test-token", sha, nil) //nolint:bodyclose // closed via getMedia t.Cleanup
	if got.StatusCode != http.StatusOK {
		t.Fatalf("fetch status = %d, want 200", got.StatusCode)
	}
	body, _ := io.ReadAll(got.Body)
	if !bytes.Equal(body, payload) {
		t.Errorf("fetched bytes = %q, want %q", body, payload)
	}
}

func TestMediaUpload_Unauthorized(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "wrong-token", []byte("x")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMediaUpload_MissingToken(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "", []byte("x")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMediaUpload_DisabledWhenNoUploader(t *testing.T) {
	srv := newUploadServer(t, "test-token", nil)

	resp := postUpload(t, srv.ListenerAddr().String(), "test-token", []byte("x")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || env.Error.Code != -32601 {
		t.Errorf("error = %+v, want code -32601", env.Error)
	}
}

func TestMediaUpload_EmptyBody(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "test-token", nil) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || env.Error.Code != -32602 {
		t.Errorf("error = %+v, want code -32602", env.Error)
	}
}

// TestMediaUpload_Oversize413 pins the 16 MiB ceiling enforced by the route's
// own MaxBytesReader — independent of the 1 MiB JSON-RPC frame cap.
func TestMediaUpload_Oversize413(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)
	// One byte over the domain ceiling.
	oversize := bytes.Repeat([]byte{0xab}, domain.MaxMediaBytes+1)

	resp := postUpload(t, srv.ListenerAddr().String(), "test-token", oversize) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

// TestMediaUpload_AtCeiling pins that a payload exactly at the ceiling is
// accepted (the cap is inclusive of MaxMediaBytes).
func TestMediaUpload_AtCeiling(t *testing.T) {
	mw := newFakeMediaWriter(t)
	srv := newUploadServer(t, "test-token", mw)
	atCap := bytes.Repeat([]byte{0xcd}, domain.MaxMediaBytes)

	resp := postUpload(t, srv.ListenerAddr().String(), "test-token", atCap) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ceiling is inclusive)", resp.StatusCode)
	}
	_, _, size := uploadResultOf(t, resp)
	if size != int64(domain.MaxMediaBytes) {
		t.Errorf("size = %d, want %d", size, domain.MaxMediaBytes)
	}
}

// --- scoped (multi-token) auth: the upload route is a send-class capability ---

// scopedWriterServer wires the upload route behind scoped auth so the scope
// gate is exercised.
func newScopedUploadServer(t *testing.T, store *fakeStore, mw *fakeMediaWriter) *Server {
	t.Helper()
	srv, err := NewServer(t.Context(), "127.0.0.1:0",
		&fakeDispatcher{}, NewScopedAuth(store),
		WithLogger(discardLogger()), WithMediaUploader(mw), WithMediaStore(mw),
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

func TestMediaUpload_ScopeSendPasses(t *testing.T) {
	store := newFakeStore()
	store.Add("send-tok", ScopeSendStr)
	mw := newFakeMediaWriter(t)
	srv := newScopedUploadServer(t, store, mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "send-tok", []byte("scoped send ok")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send-scope upload status = %d, want 200", resp.StatusCode)
	}
}

func TestMediaUpload_ScopeAdminPasses(t *testing.T) {
	store := newFakeStore()
	store.Add("adm-tok", ScopeAdminStr)
	mw := newFakeMediaWriter(t)
	srv := newScopedUploadServer(t, store, mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "adm-tok", []byte("scoped admin ok")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin-scope upload status = %d, want 200", resp.StatusCode)
	}
}

// TestMediaUpload_ScopeReadForbidden pins that a read-only token cannot
// upload — the route requires send scope (mirrors sendMedia).
func TestMediaUpload_ScopeReadForbidden(t *testing.T) {
	store := newFakeStore()
	store.Add("ro-tok", ScopeReadStr)
	mw := newFakeMediaWriter(t)
	srv := newScopedUploadServer(t, store, mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "ro-tok", []byte("nope")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only upload status = %d, want 403", resp.StatusCode)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || !strings.Contains(env.Error.Message, "scope insufficient") {
		t.Errorf("error = %+v, want scope-insufficient envelope", env.Error)
	}
}

// TestMediaUpload_ScopeRevokedUnauthorized pins that a revoked token is 401,
// not 403 — auth fails before the scope gate.
func TestMediaUpload_ScopeRevokedUnauthorized(t *testing.T) {
	store := newFakeStore()
	store.Add("send-tok", ScopeSendStr)
	store.revoked["send-tok"] = true
	mw := newFakeMediaWriter(t)
	srv := newScopedUploadServer(t, store, mw)

	resp := postUpload(t, srv.ListenerAddr().String(), "send-tok", []byte("revoked")) //nolint:bodyclose // closed via t.Cleanup
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked upload status = %d, want 401", resp.StatusCode)
	}
}
