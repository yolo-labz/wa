package socket

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"

	"github.com/creachadair/jrpc2/channel"
)

// maxFrameBytes is the hard per-frame cap on the line-delimited JSON-RPC
// transport. A single inbound frame (the bytes up to and including the
// terminating '\n') may not exceed this size. It mirrors the REST adapter's
// maxRequestBodyBytes (1 MiB) and realises the long-documented
// CodeOversizedMessage (-32004) contract, which until now was defined but
// never enforced.
//
// The cap is intentionally compatible with future media streaming over the
// socket: media is chunked so each chunk stays well under this limit. Do NOT
// raise this value for media — raise the chunk count instead.
const maxFrameBytes = 1 << 20 // 1 MiB

// errOversizedFrame is the sentinel returned by boundedChannel.Recv when a
// single inbound frame would exceed maxFrameBytes. It is reported to the
// jrpc2 server loop, which treats any Recv error as a fatal receive failure
// and tears the connection down. Before returning it, Recv writes a
// CodeOversizedMessage (-32004) error frame to the peer so the client learns
// why the connection is closing rather than seeing a bare EOF.
var errOversizedFrame = errors.New("socket: inbound frame exceeds 1 MiB cap")

// boundedChannel decorates a line-delimited jrpc2 channel.Channel with a hard
// per-frame size cap on the receive path. Send and Close delegate to the
// wrapped channel so outbound framing is byte-identical to channel.Line; only
// Recv is replaced, because channel.Line's own Recv buffers an entire line
// with no size limit (a client streaming a huge line with no '\n' would
// otherwise exhaust daemon memory — a latent DoS).
//
// Recv reads directly from the shared *bufio.Reader handed in by the
// handshake (the same reader the wrapped channel was constructed over), so no
// pipelined bytes are lost and the wrapped channel's read side is never
// exercised.
type boundedChannel struct {
	inner channel.Channel // wrapped channel; used for Send/Close only
	br    *bufio.Reader   // shared inbound reader; Recv consumes from here
	wc    io.Writer       // peer; receives the best-effort -32004 frame
	limit int             // per-frame byte cap (maxFrameBytes)
	// oversized latches once a frame has overflowed so a subsequent Recv
	// (jrpc2 only makes one before stopping, but be defensive) does not try
	// to resynchronise on a now-misaligned stream.
	oversized bool
}

// newBoundedChannel wraps line into a boundedChannel that caps each inbound
// frame at limit bytes. br must be the reader line was constructed over; wc is
// the peer connection the oversized error frame is written to.
func newBoundedChannel(line channel.Channel, br *bufio.Reader, wc io.Writer, limit int) *boundedChannel {
	return &boundedChannel{inner: line, br: br, wc: wc, limit: limit}
}

// Send implements channel.Channel by delegating to the wrapped channel.
func (c *boundedChannel) Send(msg []byte) error { return c.inner.Send(msg) }

// Close implements channel.Channel by delegating to the wrapped channel.
func (c *boundedChannel) Close() error { return c.inner.Close() }

// Recv implements channel.Channel. It reads one '\n'-terminated frame from the
// underlying buffered reader, accumulating bytes only up to the cap. If the
// frame would exceed the cap, Recv writes a -32004 error frame to the peer and
// returns errOversizedFrame without buffering the rest of the line, so a
// malicious unbounded line cannot exhaust memory. Normal (<cap) frames are
// returned exactly as channel.Line would, including the io.EOF-with-trailing
// -data convention.
func (c *boundedChannel) Recv() ([]byte, error) {
	if c.oversized {
		return nil, errOversizedFrame
	}
	buf := make([]byte, 0, 512)
	for {
		b, err := c.br.ReadByte()
		if err != nil {
			// Mirror channel.Line: a partial frame followed by EOF is
			// returned to the caller with the EOF so the last line is not
			// silently dropped.
			if len(buf) > 0 {
				return buf, err
			}
			return nil, err
		}
		if b == '\n' {
			return buf, nil
		}
		if len(buf) >= c.limit {
			// One more byte would push the frame (content + the '\n' we have
			// not yet seen) past the cap. Reject before buffering further.
			c.oversized = true
			c.writeOversizedError()
			return nil, errOversizedFrame
		}
		buf = append(buf, b)
	}
}

// writeOversizedError emits a best-effort CodeOversizedMessage (-32004) frame
// to the peer. It follows the same newline-delimited, best-effort pattern as
// writeHelloError and backpressureFrame elsewhere in this package: a failed
// write is ignored because the connection is being torn down regardless.
func (c *boundedChannel) writeOversizedError() {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeOversizedMessage),
			"message": errCodeName[CodeOversizedMessage],
			"data":    map[string]any{"reason": "frame exceeds 1 MiB cap"},
		},
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = c.wc.Write(data)
}
