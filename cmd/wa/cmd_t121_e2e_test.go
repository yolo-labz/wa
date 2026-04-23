// Package main — end-to-end tests for feature 017 T1-21 CLI subcommands.
// Matches tasks-tier1.md row T1-21 test names:
//   - TestWaContactsLookup
//   - TestWaDraftApproveRejectFlow
//   - TestWaMediaDownloadPrintsPath
//
// Each test stands up a fake JSON-RPC server on a throwaway unix socket,
// invokes the cobra command tree in-process (matches cmd_profile_e2e_test
// harness), and asserts on stdout/stderr.
package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// rpcCall is one JSON-RPC request seen by the fake server.
type rpcCall struct {
	Method string
	Params json.RawMessage
}

// fakeDaemon is a minimal JSON-RPC 2.0 server for CLI tests. The handler
// map is keyed by method name and returns (result, error) per JSON-RPC
// semantics.
type fakeDaemon struct {
	t        *testing.T
	handlers map[string]func(params json.RawMessage) (any, *rpcError)

	mu    sync.Mutex
	calls []rpcCall
	ln    net.Listener
	done  chan struct{}
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	// Use os.MkdirTemp (short name) instead of t.TempDir() which embeds
	// the test name; long test names blow past macOS's 104-byte
	// sun_path limit on unix sockets.
	dir, err := os.MkdirTemp("", "wa-fd-*")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "wa.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fd := &fakeDaemon{
		t:        t,
		handlers: map[string]func(json.RawMessage) (any, *rpcError){},
		ln:       ln,
		done:     make(chan struct{}),
	}
	// Register a default system.hello handler so tests exercise the
	// handshake-gated code path the daemon enforces since v2.0.0
	// without each test having to re-register it. Individual tests can
	// still override this via on().
	fd.on("system.hello", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"serverVersion": "test", "acceptedProtoVersion": 2}, nil
	})
	go fd.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		<-fd.done
	})
	return fd
}

func (f *fakeDaemon) path() string {
	return f.ln.Addr().String()
}

func (f *fakeDaemon) on(method string, h func(params json.RawMessage) (any, *rpcError)) {
	f.handlers[method] = h
}

func (f *fakeDaemon) seen() []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rpcCall(nil), f.calls...)
}

func (f *fakeDaemon) serve() {
	defer close(f.done)
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handleConn(conn)
	}
}

func (f *fakeDaemon) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Don't record the FR-012 handshake in the call list — every CLI
		// invocation sends it, and per-test seen()/call-count assertions
		// in the existing suite are counting user-level RPCs only.
		if req.Method != "system.hello" {
			f.mu.Lock()
			f.calls = append(f.calls, rpcCall{Method: req.Method, Params: rawParams(req.Params)})
			f.mu.Unlock()
		}

		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		h, ok := f.handlers[req.Method]
		if !ok {
			resp.Error = &rpcError{Code: -32601, Message: "MethodNotFound"}
		} else {
			res, rpcErr := h(rawParams(req.Params))
			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				raw, err := json.Marshal(res)
				if err != nil {
					resp.Error = &rpcError{Code: -32603, Message: err.Error()}
				} else {
					resp.Result = raw
				}
			}
		}
		out, err := json.Marshal(resp)
		if err != nil {
			return
		}
		if _, err := conn.Write(append(out, '\n')); err != nil {
			return
		}
	}
}

func rawParams(p any) json.RawMessage {
	switch v := p.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return v
	case []byte:
		return json.RawMessage(v)
	default:
		b, _ := json.Marshal(v)
		return b
	}
}

// TestWaContactsLookup exercises `wa contacts lookup --jid <j>` — verifies
// the CLI emits a single JSON-RPC contacts.lookup request and prints the
// daemon's response.
func TestWaContactsLookup(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("contacts.lookup", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			JID string `json:"jid"`
		}
		_ = json.Unmarshal(params, &p)
		if p.JID == "" {
			return nil, &rpcError{Code: -32602, Message: "jid required"}
		}
		return map[string]any{
			"jid":         p.JID,
			"displayName": "Bob",
		}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"contacts", "lookup",
		"--jid", "5511900000000@s.whatsapp.net",
	)

	if !strings.Contains(stdout, `"displayName":"Bob"`) {
		t.Fatalf("stdout missing displayName:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, `"schema":"wa.contacts.lookup/v1"`) {
		t.Fatalf("stdout missing schema envelope:\nstdout=%q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "contacts.lookup" {
		t.Fatalf("expected 1 contacts.lookup call, got %+v", calls)
	}
}

// TestWaDraftApproveRejectFlow runs approve then reject against the same
// draft id via the CLI; verifies method names, default decider "cli", and
// that the reason flag is threaded through.
func TestWaDraftApproveRejectFlow(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("draft.approve", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID      string `json:"id"`
			Decider string `json:"decider"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ID != "drf_01" {
			return nil, &rpcError{Code: -32602, Message: "wrong id"}
		}
		if p.Decider != "cli" {
			return nil, &rpcError{Code: -32602, Message: "wrong decider"}
		}
		return map[string]any{"state": "approved", "id": p.ID}, nil
	})
	fd.on("draft.reject", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID      string `json:"id"`
			Decider string `json:"decider"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Reason != "stale" {
			return nil, &rpcError{Code: -32602, Message: "missing reason"}
		}
		return map[string]any{"state": "rejected", "id": p.ID}, nil
	})

	outA, errA := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"draft", "approve",
		"--id", "drf_01",
	)
	if !strings.Contains(outA, `"state":"approved"`) {
		t.Fatalf("approve stdout missing:\nstdout=%q\nstderr=%q", outA, errA)
	}

	outR, errR := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"draft", "reject",
		"--id", "drf_01",
		"--reason", "stale",
	)
	if !strings.Contains(outR, `"state":"rejected"`) {
		t.Fatalf("reject stdout missing:\nstdout=%q\nstderr=%q", outR, errR)
	}

	calls := fd.seen()
	if len(calls) != 2 {
		t.Fatalf("expected 2 rpc calls, got %d: %+v", len(calls), calls)
	}
	if calls[0].Method != "draft.approve" || calls[1].Method != "draft.reject" {
		t.Fatalf("method sequence wrong: %+v", calls)
	}
}

// TestWaMediaDownloadPrintsPath asserts FR-050a: in human mode, `wa media
// download` prints ONLY the on-disk path on stdout (no JSON envelope).
func TestWaMediaDownloadPrintsPath(t *testing.T) {
	fd := newFakeDaemon(t)
	diskPath := filepath.Join(os.TempDir(), "wa-media-test.ogg")
	fd.on("media.download", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			MessageID string `json:"messageId"`
		}
		_ = json.Unmarshal(params, &p)
		if p.MessageID == "" {
			return nil, &rpcError{Code: -32602, Message: "missing messageId"}
		}
		return map[string]any{
			"object": map[string]any{
				"sha256": "deadbeef",
				"path":   diskPath,
				"mime":   "audio/ogg",
			},
		}, nil
	})

	// Reset flagJSON so a prior --json test does not pollute this invocation;
	// runCmd saves profile/socket but not json.
	prevJSON := flagJSON
	flagJSON = false
	defer func() { flagJSON = prevJSON }()

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"media", "download",
		"--message-id", "msg_01",
	)
	got := strings.TrimSpace(stdout)
	if got != diskPath {
		t.Fatalf("stdout mismatch:\nwant=%q\ngot=%q\nstderr=%q", diskPath, got, stderr)
	}
	if strings.Contains(got, "{") || strings.Contains(got, "schema") {
		t.Fatalf("FR-050a violation — stdout contains JSON: %q", got)
	}
}
