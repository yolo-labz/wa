// Package main — end-to-end tests for the spec-198 client-local media upload
// surface: `wa push <file>` and `wa sendMedia --remote` transparently
// uploading a client-local file then sending by sha256. Drives the in-process
// cobra command (runCmd) against an httptest server that stands in for the
// daemon's REST adapter (POST /media/upload + POST /v1/rpc).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRemote is an httptest server emulating the daemon's REST adapter for
// the upload + sendMedia flow. It content-addresses uploaded bodies and
// records every method call so a test can assert the two-call sequence.
type fakeRemote struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	uploads  [][]byte         // raw bodies received on POST /media/upload
	rpcCalls []fakeRemoteCall // method+params received on POST /v1/rpc
}

type fakeRemoteCall struct {
	Method string
	Params json.RawMessage
}

func newFakeRemote(t *testing.T) *fakeRemote {
	t.Helper()
	fr := &fakeRemote{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /media/upload", fr.handleUpload)
	mux.HandleFunc("POST /v1/rpc", fr.handleRPC)
	fr.srv = httptest.NewServer(mux)
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRemote) url() string { return fr.srv.URL }

func (fr *fakeRemote) handleUpload(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		fr.t.Errorf("upload Authorization = %q, want Bearer test-token", got)
	}
	body, _ := io.ReadAll(r.Body)
	fr.mu.Lock()
	fr.uploads = append(fr.uploads, body)
	fr.mu.Unlock()
	sum := sha256.Sum256(body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema": "wa.media.upload/v1",
		"sha256": hex.EncodeToString(sum[:]),
		"size":   len(body),
	})
}

func (fr *fakeRemote) handleRPC(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     json.RawMessage `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	fr.mu.Lock()
	fr.rpcCalls = append(fr.rpcCalls, fakeRemoteCall{Method: req.Method, Params: req.Params})
	fr.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"messageId":"wamid.SENT","timestamp":123}}`))
}

func (fr *fakeRemote) seenUploads() [][]byte {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return fr.uploads
}

func (fr *fakeRemote) seenRPC() []fakeRemoteCall {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return fr.rpcCalls
}

func writeTempFile(t *testing.T, payload []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(p, payload, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

// TestSendMediaRemote_UploadsThenSendsBySHA pins the spec-198 CLI flow: a
// `--remote sendMedia --path <localfile>` first POSTs the bytes to
// /media/upload, then calls sendMedia with the returned sha256 (NOT the path).
func TestSendMediaRemote_UploadsThenSendsBySHA(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")
	t.Setenv("WA_TOKEN", "test-token")
	fr := newFakeRemote(t)

	payload := []byte("client-local jpeg bytes \xff\xd8\xff\xe0")
	file := writeTempFile(t, payload)
	wantSHA := sha256.Sum256(payload)

	stdout, stderr := runCmd(t, "--remote", fr.url(), "sendMedia",
		"--to", "5581998398677@s.whatsapp.net", "--path", file, "--caption", "hi")

	uploads := fr.seenUploads()
	if len(uploads) != 1 {
		t.Fatalf("want 1 upload, got %d (stderr=%q)", len(uploads), stderr)
	}
	if string(uploads[0]) != string(payload) {
		t.Errorf("uploaded bytes mismatch: got %q, want %q", uploads[0], payload)
	}

	rpc := fr.seenRPC()
	if len(rpc) != 1 || rpc[0].Method != "sendMedia" {
		t.Fatalf("want 1 sendMedia RPC, got %+v", rpc)
	}
	var p struct {
		To      string `json:"to"`
		Path    string `json:"path"`
		SHA256  string `json:"sha256"`
		Caption string `json:"caption"`
	}
	if err := json.Unmarshal(rpc[0].Params, &p); err != nil {
		t.Fatalf("decode sendMedia params: %v", err)
	}
	if p.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("sendMedia sha256 = %q, want %q", p.SHA256, hex.EncodeToString(wantSHA[:]))
	}
	if p.Path != "" {
		t.Errorf("sendMedia must NOT carry the client path in remote mode; got %q", p.Path)
	}
	if p.Caption != "hi" {
		t.Errorf("caption = %q, want hi", p.Caption)
	}
	if !strings.Contains(stdout, "wamid.SENT") {
		t.Errorf("stdout = %q, want it to mention the sent message id", stdout)
	}
}

// TestPush_PrintsSHA pins `wa push <file>` uploading the bytes and printing
// the sha256 on stdout.
func TestPush_PrintsSHA(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")
	t.Setenv("WA_TOKEN", "test-token")
	fr := newFakeRemote(t)

	payload := []byte("stage me for reuse")
	file := writeTempFile(t, payload)
	wantSHA := hex.EncodeToString(func() []byte { s := sha256.Sum256(payload); return s[:] }())

	stdout, stderr := runCmd(t, "--remote", fr.url(), "push", file)

	if len(fr.seenUploads()) != 1 {
		t.Fatalf("want 1 upload, got %d (stderr=%q)", len(fr.seenUploads()), stderr)
	}
	if strings.TrimSpace(stdout) != wantSHA {
		t.Errorf("stdout = %q, want sha %q", strings.TrimSpace(stdout), wantSHA)
	}
}

// TestPush_JSONMode pins `wa push --json` emitting the upload envelope.
func TestPush_JSONMode(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")
	t.Setenv("WA_TOKEN", "test-token")
	fr := newFakeRemote(t)

	payload := []byte("json mode push")
	file := writeTempFile(t, payload)

	stdout, _ := runCmd(t, "--remote", fr.url(), "--json", "push", file)

	var out struct {
		Schema string `json:"schema"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		t.Fatalf("decode --json push output %q: %v", stdout, err)
	}
	if out.Schema != "wa.media.upload/v1" {
		t.Errorf("schema = %q, want wa.media.upload/v1", out.Schema)
	}
	if out.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", out.Size, len(payload))
	}
}

// TestPush_RequiresRemote pins that `wa push` without --remote fails with a
// usage error (local mode has nothing to upload).
func TestPush_RequiresRemote(t *testing.T) {
	file := writeTempFile(t, []byte("x"))
	stdout, stderr := runCmd(t, "push", file)
	combined := stdout + stderr
	if !strings.Contains(combined, "--remote is required") {
		t.Errorf("want a --remote-required usage error, got:\n%s", combined)
	}
}

// TestSendMediaLocal_KeepsPathBehaviour pins that WITHOUT --remote, sendMedia
// forwards the path unchanged over the socket and never uploads.
func TestSendMediaLocal_KeepsPathBehaviour(t *testing.T) {
	fd := newFakeDaemon(t)
	var gotPath, gotSHA string
	fd.on("sendMedia", func(raw json.RawMessage) (any, *rpcError) {
		var p struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}
		_ = json.Unmarshal(raw, &p)
		gotPath, gotSHA = p.Path, p.SHA256
		return map[string]any{"messageId": "wamid.LOCAL", "timestamp": 1}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "sendMedia",
		"--to", "5581998398677@s.whatsapp.net", "--path", "/daemon/side/file.jpg")

	if gotPath != "/daemon/side/file.jpg" {
		t.Errorf("local sendMedia path = %q, want the daemon-side path unchanged (stderr=%q)", gotPath, stderr)
	}
	if gotSHA != "" {
		t.Errorf("local sendMedia must not set sha256; got %q", gotSHA)
	}
}
