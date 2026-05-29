package rest

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeMediaResolver is a MediaResolver double for the GET /media/{sha256}
// handler tests. A non-nil err is returned verbatim so a test can assert
// the os.ErrNotExist → 404 mapping.
type fakeMediaResolver struct {
	obj domain.MediaObject
	err error
}

func (f *fakeMediaResolver) Resolve(_ context.Context, _ [32]byte) (domain.MediaObject, error) {
	if f.err != nil {
		return domain.MediaObject{}, f.err
	}
	return f.obj, nil
}

func newMediaServer(t *testing.T, token string, mr MediaResolver) *Server {
	t.Helper()
	opts := []ServerOption{WithLogger(discardLogger())}
	if mr != nil {
		opts = append(opts, WithMediaStore(mr))
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

func getMedia(t *testing.T, addr, token, shaHex string, hdr http.Header) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/media/"+shaHex, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// writeTempMedia drops a payload into a temp file and returns its path
// plus a MediaObject pointing at it.
func writeTempMedia(t *testing.T, shaHex, mime string, payload []byte) domain.MediaObject {
	t.Helper()
	path := filepath.Join(t.TempDir(), shaHex+".bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write temp media: %v", err)
	}
	var sha [32]byte
	return domain.MediaObject{
		Ref:          domain.MediaRef{SHA256: sha, Mime: mime, Size: int64(len(payload)), Ext: "bin"},
		Path:         path,
		MimeDetected: mime,
		FetchedAt:    time.Now().Unix(),
	}
}

const testSHA = "abababababababababababababababababababababababababababababababab" // 64 hex

func TestMediaFetch_OK(t *testing.T) {
	payload := []byte("hello media bytes")
	mime := "text/plain; charset=utf-8"
	mr := &fakeMediaResolver{obj: writeTempMedia(t, testSHA, mime, payload)}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", testSHA, nil) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != mime {
		t.Errorf("Content-Type = %q, want %q", ct, mime)
	}
	if et := resp.Header.Get("ETag"); et != `"`+testSHA+`"` {
		t.Errorf("ETag = %q, want %q", et, `"`+testSHA+`"`)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want it to contain immutable", cc)
	}
	if nosniff := resp.Header.Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(payload) {
		t.Errorf("body = %q, want %q", body, payload)
	}
}

func TestMediaFetch_NotFound(t *testing.T) {
	mr := &fakeMediaResolver{err: os.ErrNotExist}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", testSHA, nil) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || env.Error.Code != -32099 {
		t.Errorf("error = %+v, want code -32099", env.Error)
	}
}

func TestMediaFetch_Unauthorized(t *testing.T) {
	mr := &fakeMediaResolver{obj: writeTempMedia(t, testSHA, "text/plain", []byte("x"))}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "wrong-token", testSHA, nil) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMediaFetch_DisabledWhenNoStore(t *testing.T) {
	srv := newMediaServer(t, "test-token", nil)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", testSHA, nil) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || env.Error.Code != -32601 {
		t.Errorf("error = %+v, want code -32601", env.Error)
	}
}

func TestMediaFetch_BadSHA(t *testing.T) {
	mr := &fakeMediaResolver{obj: writeTempMedia(t, testSHA, "text/plain", []byte("x"))}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", "not-a-valid-sha", nil) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	env := decodeResp(t, resp)
	if env.Error == nil || env.Error.Code != -32602 {
		t.Errorf("error = %+v, want code -32602", env.Error)
	}
}

func TestMediaFetch_RangeRequest(t *testing.T) {
	payload := []byte("0123456789abcdef")
	mr := &fakeMediaResolver{obj: writeTempMedia(t, testSHA, "application/octet-stream", payload)}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", testSHA, http.Header{"Range": {"bytes=0-4"}}) //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); cr == "" {
		t.Error("missing Content-Range header on 206")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "01234" {
		t.Errorf("partial body = %q, want %q", body, "01234")
	}
}

func TestMediaFetch_IfNoneMatchNotModified(t *testing.T) {
	mr := &fakeMediaResolver{obj: writeTempMedia(t, testSHA, "text/plain", []byte("cached"))}
	srv := newMediaServer(t, "test-token", mr)

	resp := getMedia(t, srv.ListenerAddr().String(), "test-token", testSHA, //nolint:bodyclose // body closed by getMedia helper via t.Cleanup
		http.Header{"If-None-Match": {`"` + testSHA + `"`}})
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
}
