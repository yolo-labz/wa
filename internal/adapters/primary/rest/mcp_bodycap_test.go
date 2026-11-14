package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// padReader streams a JSON-RPC frame of n bytes without materialising it,
// so the allocation the test measures is the server's, not the client's.
type padReader struct{ head, left int }

func (p *padReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	n := min(len(b), p.left)
	for i := range n {
		b[i] = 'a'
	}
	if p.head > 0 { // a plausible JSON-RPC prefix, then padding
		n = copy(b, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"p":"`)
		p.head = 0
	}
	p.left -= n
	return n, nil
}

// TestMCPRoute_BodyCapped pins WA-MCP-SEC F2: a 64 MiB streamed (chunked)
// body to /mcp answers 413 before the MCP handler runs, and the daemon's
// allocation stays bounded by the 1 MiB cap instead of the body size.
func TestMCPRoute_BodyCapped(t *testing.T) {
	var reached atomic.Int32
	provider := func(Scope) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		})
	}
	token := strings.Repeat("a", 64)
	srv, err := NewServer(t.Context(), "127.0.0.1:0", &fakeDispatcher{}, NewEnvTokenAuth(token),
		WithLogger(discardLogger()), WithMCP(provider))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	post := func(body io.Reader) int {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+srv.ListenerAddr().String()+"/mcp", body)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	status := post(&padReader{head: 1, left: 64 << 20})
	runtime.ReadMemStats(&after)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("64 MiB body: status %d, want 413", status)
	}
	if reached.Load() != 0 {
		t.Fatal("oversize body reached the MCP handler")
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Fatalf("64 MiB body allocated %d MiB, want <= 16", grew>>20)
	}

	// A frame under the cap is replayed to the handler intact.
	if status := post(&padReader{head: 1, left: 512 << 10}); status != http.StatusOK || reached.Load() != 1 {
		t.Fatalf("512 KiB body: status %d reached %d, want 200 and 1", status, reached.Load())
	}
}

// TestRPC_MarksRemoteCaller pins the F1 transport split: /v1/rpc dispatches
// with the network-caller mark that confines daemon-host media paths.
func TestRPC_MarksRemoteCaller(t *testing.T) {
	var remote atomic.Bool
	d := &fakeDispatcher{handler: func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		remote.Store(app.IsRemoteCaller(ctx))
		return json.RawMessage(`{}`), nil
	}}
	token := strings.Repeat("b", 64)
	srv, err := NewServer(t.Context(), "127.0.0.1:0", d, NewEnvTokenAuth(token), WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+srv.ListenerAddr().String()+"/v1/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"status"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/rpc: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !remote.Load() {
		t.Fatalf("status %d remote=%v, want 200 and a remote-marked dispatch", resp.StatusCode, remote.Load())
	}
}
