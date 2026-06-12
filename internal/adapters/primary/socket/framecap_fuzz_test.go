package socket

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2/channel"
)

// nopWriteCloser adapts io.Discard to the io.WriteCloser channel.Line
// requires; the inner channel's write side is never exercised here.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// FuzzFrameRecv asserts the bounded framing contract over arbitrary
// inbound byte streams and frame caps:
//
//  1. Recv never returns a frame longer than the cap and never returns
//     a frame containing the '\n' delimiter.
//  2. An over-cap line yields errOversizedFrame, latches (every later
//     Recv fails the same way), and writes exactly one best-effort
//     CodeOversizedMessage (-32004) frame to the peer.
//  3. Streams within the cap never trigger the -32004 path.
//  4. Recv always terminates — no input can wedge the read loop.
//
// Seeds cover empty input, normal frames, the partial-frame-at-EOF
// convention, exactly-at-cap lines, and over-cap floods.
func FuzzFrameRecv(f *testing.F) {
	f.Add([]byte(nil), uint16(0))
	f.Add([]byte("hello\n"), uint16(16))
	f.Add([]byte("a\nb\nc"), uint16(4))
	f.Add([]byte("\n\n\n"), uint16(1))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"send","id":1}`+"\n"), uint16(64))
	f.Add([]byte(strings.Repeat("x", 64)+"\n"), uint16(63))
	f.Add([]byte(strings.Repeat("x", 65)+"\n"), uint16(63))
	f.Add([]byte(strings.Repeat("\x00", 256)), uint16(8))

	f.Fuzz(func(t *testing.T, data []byte, capRaw uint16) {
		limit := int(capRaw)%512 + 1
		br := bufio.NewReader(bytes.NewReader(data))
		var peer bytes.Buffer
		inner := channel.Line(bytes.NewReader(nil), nopWriteCloser{io.Discard})
		ch := newBoundedChannel(inner, br, &peer, limit)

		sawOversized := false
		// Each Recv consumes at least one byte or returns an error, so
		// len(data)+2 iterations is a strict upper bound; exceeding it
		// means the loop wedged.
		for i := 0; i <= len(data)+2; i++ {
			frame, err := ch.Recv()
			if len(frame) > limit {
				t.Fatalf("frame of %d bytes exceeds cap %d", len(frame), limit)
			}
			if bytes.ContainsRune(frame, '\n') {
				t.Fatalf("frame contains delimiter: %q", frame)
			}
			if err == nil {
				continue
			}
			if errors.Is(err, errOversizedFrame) {
				sawOversized = true
				if _, err2 := ch.Recv(); !errors.Is(err2, errOversizedFrame) {
					t.Fatalf("oversized latch broken: second Recv = %v", err2)
				}
			}
			break
		}

		out := peer.String()
		if sawOversized {
			if n := strings.Count(out, `"code":-32004`); n != 1 {
				t.Fatalf("want exactly one -32004 frame after overflow, got %d: %q", n, out)
			}
		} else if out != "" {
			t.Fatalf("peer write without overflow: %q", out)
		}
	})
}
