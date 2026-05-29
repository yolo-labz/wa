package sockettest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
)

// frameCapBytes mirrors socket.maxFrameBytes (1 MiB). It is duplicated here
// rather than exported from the production package because the cap is an
// internal transport invariant; the test asserts the documented 1 MiB
// boundary directly.
const frameCapBytes = 1 << 20 // 1 MiB

// sizeEchoSetup registers a `size` handler that reports back the byte length
// of the params it received. The response is tiny regardless of request size,
// so a near-cap inbound frame can be proven to reach dispatch without forcing
// the test harness scanner to read a megabyte-sized response.
func sizeEchoSetup(d *FakeDispatcher) {
	d.On("size", func(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]int{"len": len(params)})
	})
}

// TestFrameCap_UnderLimitProcessedNormally proves a frame just under the
// 1 MiB cap flows through the bounded channel unchanged: the handler runs and
// observes the full inbound payload. The handler returns only the byte count,
// keeping the response small enough for the harness scanner.
func TestFrameCap_UnderLimitProcessedNormally(t *testing.T) {
	_, path := startServer(t, sizeEchoSetup)
	conn, scanner := dial(t, path)

	// Build a single-line request whose total framed size (the line plus the
	// terminating '\n' the harness appends) is just under the cap.
	const envelopeOverhead = 128 // jsonrpc/id/method/params scaffolding + quotes
	payloadLen := frameCapBytes - envelopeOverhead
	big := strings.Repeat("a", payloadLen)
	line := `{"jsonrpc":"2.0","id":1,"method":"size","params":{"blob":"` + big + `"}}`
	framed := len(line) + 1 // sendLine appends '\n'
	if framed > frameCapBytes {
		t.Fatalf("test bug: framed line %d exceeds cap %d", framed, frameCapBytes)
	}
	if framed < frameCapBytes-1024 {
		t.Fatalf("test bug: framed line %d is not close enough to the cap %d", framed, frameCapBytes)
	}

	sendLine(t, conn, line)
	resp := recvResponse(t, scanner)

	if resp.Error != nil {
		t.Fatalf("under-cap frame rejected: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}
	var result struct {
		Len int `json:"len"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	// params is the object after "params": — i.e. {"blob":"aaa…"}. It must
	// carry the whole payload, proving the bounded reader passed it through.
	if result.Len < payloadLen {
		t.Errorf("handler saw params len %d, want >= %d (payload truncated?)", result.Len, payloadLen)
	}
}

// TestFrameCap_OverLimitRejectedWithOversized proves a single frame exceeding
// the 1 MiB cap is rejected with CodeOversizedMessage (-32004) and the
// connection is closed, without the server buffering the whole oversized line
// (the bounded reader stops at the cap, so no OOM).
func TestFrameCap_OverLimitRejectedWithOversized(t *testing.T) {
	_, path := startServer(t, sizeEchoSetup)
	conn, scanner := dial(t, path)

	// A frame comfortably over the cap, with NO terminating newline yet — this
	// is the unbounded-buffer DoS shape. The bounded reader must reject it
	// after reading at most ~cap bytes, never the full payload.
	oversized := 2 * frameCapBytes
	big := strings.Repeat("a", oversized)
	line := `{"jsonrpc":"2.0","id":1,"method":"size","params":{"blob":"` + big

	// Write the oversized frame. The server caps the read and closes the
	// connection, so our write may fail partway with a broken pipe — that is
	// the expected outcome, not a test failure.
	if _, err := fmt.Fprint(conn, line); err != nil {
		t.Logf("write of oversized frame errored as expected: %v", err)
	}

	// The server writes a -32004 error frame before closing the connection.
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("reading oversized rejection: %v", err)
		}
		t.Fatal("connection closed without sending an oversized-message error frame")
	}
	var resp rpcResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode oversized rejection %q: %v", scanner.Text(), err)
	}
	if resp.Error == nil {
		t.Fatalf("oversized frame accepted: %+v", resp)
	}
	if resp.Error.Code != int(socket.CodeOversizedMessage) {
		t.Errorf("error.code = %d, want %d (CodeOversizedMessage)", resp.Error.Code, int(socket.CodeOversizedMessage))
	}

	// After the oversized frame the connection must be closed: the next read
	// returns no further line.
	if scanner.Scan() {
		t.Errorf("connection still open after oversized frame; read extra line %q", scanner.Text())
	}
}
