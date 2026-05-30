// Package main — end-to-end tests for `wa media fetch` over the local unix
// socket (audit #4): the CLI must loop media.fetchBytes (offset += len until
// eof) and reconstruct the exact object bytes, replacing the old os.Open of a
// daemon-side path that does not exist on the client host.
//
// Reuses the fakeDaemon / runCmd harness (cmd_t121_e2e_test.go).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chunkServer is the fake daemon's media.fetchBytes handler: it serves windows
// of payload, clamping each response to chunkCeiling so the CLI is forced to
// loop for anything larger.
func chunkServer(payload []byte, chunkCeiling int64) func(json.RawMessage) (any, *rpcError) {
	return func(raw json.RawMessage) (any, *rpcError) {
		var p struct {
			SHA256 string `json:"sha256"`
			Offset int64  `json:"offset"`
			Length int64  `json:"length"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "bad params"}
		}
		size := int64(len(payload))
		if p.Offset < 0 || p.Length < 0 {
			return nil, &rpcError{Code: -32602, Message: "negative"}
		}
		if p.Offset >= size {
			return map[string]any{
				"schema": "wa.media.fetchbytes/v1", "sha256": p.SHA256,
				"size": size, "offset": p.Offset, "bytes": []byte{}, "eof": true,
			}, nil
		}
		want := p.Length
		if want == 0 || want > chunkCeiling {
			want = chunkCeiling
		}
		if rem := size - p.Offset; want > rem {
			want = rem
		}
		chunk := payload[p.Offset : p.Offset+want]
		return map[string]any{
			"schema": "wa.media.fetchbytes/v1", "sha256": p.SHA256,
			"size": size, "offset": p.Offset,
			"bytes": chunk, // []byte → base64 on the wire, decoded client-side
			"eof":   p.Offset+want >= size,
		}, nil
	}
}

// TestWaMediaFetchSocketReconstructsMultiChunk drives `wa media fetch --sha256
// --out` over the socket against a payload spanning multiple chunks and asserts
// the written file is byte-identical to the source.
func TestWaMediaFetchSocketReconstructsMultiChunk(t *testing.T) {
	// 2.5 small chunks so the loop runs ≥3 times with a tiny ceiling.
	const ceiling = 1000
	payload := make([]byte, ceiling*2+ceiling/2)
	for i := range payload {
		payload[i] = byte(i*13 + 5)
	}
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	fd := newFakeDaemon(t)
	var fetchCalls int
	fd.on("media.fetchBytes", func(raw json.RawMessage) (any, *rpcError) {
		fetchCalls++
		return chunkServer(payload, ceiling)(raw)
	})

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "video.bin")

	prevJSON := flagJSON
	flagJSON = false
	defer func() { flagJSON = prevJSON }()

	_, stderr := runCmd(t, "--socket", fd.path(),
		"media", "fetch", "--sha256", sha, "--out", outPath)

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out: %v (stderr=%q)", err, stderr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reconstructed file differs: got %d bytes want %d", len(got), len(payload))
	}
	if fetchCalls < 3 {
		t.Fatalf("expected ≥3 fetchBytes calls for a 2.5×ceiling payload, got %d", fetchCalls)
	}
	// Every recorded RPC must be media.fetchBytes (no media.resolve / os.Open).
	for _, c := range fd.seen() {
		if c.Method != "media.fetchBytes" {
			t.Fatalf("unexpected RPC %q — socket fetch must only call media.fetchBytes", c.Method)
		}
	}
	if !strings.Contains(stderr, "wrote "+outPath) {
		t.Fatalf("missing 'wrote' confirmation on stderr: %q", stderr)
	}
}

// TestWaMediaFetchSocketStdout drives a single-chunk fetch to stdout and checks
// the raw bytes land there with no JSON envelope.
func TestWaMediaFetchSocketStdout(t *testing.T) {
	payload := []byte("a small cached object that fits in one frame")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	fd := newFakeDaemon(t)
	fd.on("media.fetchBytes", chunkServer(payload, 1<<20))

	prevJSON := flagJSON
	flagJSON = false
	defer func() { flagJSON = prevJSON }()

	stdout, stderr := runCmd(t, "--socket", fd.path(),
		"media", "fetch", "--sha256", sha)

	if stdout != string(payload) {
		t.Fatalf("stdout mismatch:\n got %q\nwant %q\nstderr=%q", stdout, payload, stderr)
	}
}

// TestWaMediaFetchSocketByMessageID exercises the message-id path: the CLI
// first media.download (to learn + cache the sha), then loops media.fetchBytes
// against that sha over the socket.
func TestWaMediaFetchSocketByMessageID(t *testing.T) {
	payload := []byte("voice note bytes downloaded then fetched by sha")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	fd := newFakeDaemon(t)
	fd.on("media.download", func(raw json.RawMessage) (any, *rpcError) {
		var p struct {
			MessageID string `json:"messageId"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.MessageID == "" {
			return nil, &rpcError{Code: -32602, Message: "missing messageId"}
		}
		return map[string]any{"object": map[string]any{"sha256": sha, "path": "/daemon/only/path.bin"}}, nil
	})
	fd.on("media.fetchBytes", chunkServer(payload, 1<<20))

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "note.bin")

	prevJSON := flagJSON
	flagJSON = false
	defer func() { flagJSON = prevJSON }()

	_, stderr := runCmd(t, "--socket", fd.path(),
		"media", "fetch", "--message-id", "msg_42", "--out", outPath)

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out: %v (stderr=%q)", err, stderr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reconstructed file differs from payload")
	}
	calls := fd.seen()
	if len(calls) < 2 {
		t.Fatalf("expected media.download then ≥1 media.fetchBytes, got %+v", calls)
	}
	if calls[0].Method != "media.download" {
		t.Fatalf("first RPC = %q want media.download", calls[0].Method)
	}
	var sawFetch bool
	for _, c := range calls[1:] {
		if c.Method == "media.fetchBytes" {
			sawFetch = true
		}
	}
	if !sawFetch {
		t.Fatalf("message-id path never called media.fetchBytes: %+v", calls)
	}
}

// TestWaMediaFetchSocketRPCError maps a daemon -32301 (not cached) to the CLI's
// generic runtime exit, proving the loop surfaces RPC errors instead of writing
// a truncated file.
func TestWaMediaFetchSocketRPCError(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.fetchBytes", func(json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32301, Message: "MediaNotCached"}
	})

	sha := hex.EncodeToString(make([]byte, 32))

	prevJSON := flagJSON
	flagJSON = false
	defer func() { flagJSON = prevJSON }()

	_, stderr := runCmd(t, "--socket", fd.path(),
		"media", "fetch", "--sha256", sha)

	if !strings.Contains(stderr, "exec error") {
		t.Fatalf("RPC error should exit non-zero, stderr=%q", stderr)
	}
}
