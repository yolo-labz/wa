package socket

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// HelloBudget is the maximum wall-clock time from accept(2) the server
// waits for a client's first frame to arrive. FR-012 requires a 5s
// budget; exceeding it yields -32000 protocol_mismatch. Tests can
// override via WithHelloBudget — production code should not.
const HelloBudget = 5 * time.Second

// semverRE matches the pure-semver subset the hello response is allowed
// to expose: three dot-separated numeric components plus an optional
// pre-release/build tail. The FR-012 contract forbids commit SHAs, Go
// versions, or build dates in serverVersion — those leak implementation
// details through a wire-protocol field that agents hash for pinning.
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// helloRequest mirrors the JSON-RPC 2.0 first frame carried by every
// client on accept. Only the shape is validated here; the hello handler
// does not enter the full dispatch table.
type helloRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      json.RawMessage `json:"id"`
	Params  struct {
		ProtoVersion int `json:"protoVersion"`
	} `json:"params"`
}

// helloResult is the success payload written back on a valid handshake.
// acceptedProtoVersion echoes the negotiated protocol version so future
// clients can assert the server is running the major version they
// compiled against.
type helloResult struct {
	ServerVersion        string `json:"serverVersion"`
	AcceptedProtoVersion int    `json:"acceptedProtoVersion"`
}

// errHelloTimeout and errHelloBadFrame classify the two failure modes
// distinguishable only by the server for logging. Both surface to the
// client as -32000 protocol_mismatch per FR-012.
var (
	errHelloTimeout  = errors.New("socket: hello handshake timed out")
	errHelloBadFrame = errors.New("socket: hello handshake rejected: malformed frame")
)

// handshake performs the system.hello gate described in FR-012. It
// reads the first line from raw with a read deadline of budget from
// now, validates the JSON-RPC 2.0 shape, confirms the method is
// `system.hello` and the `protoVersion` matches domain.ProtoVersion,
// writes a success or error response, and returns a bufio.Reader that
// the caller hands to the jrpc2 channel so no bytes are lost.
//
// On failure, the connection is left open so the error frame flushes;
// the caller must close raw afterward.
func handshake(raw *net.UnixConn, serverVersion string, budget time.Duration) (*bufio.Reader, error) {
	if budget <= 0 {
		budget = HelloBudget
	}
	if err := raw.SetReadDeadline(time.Now().Add(budget)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	br := bufio.NewReader(raw)
	line, err := br.ReadBytes('\n')
	if err != nil {
		writeHelloError(raw, nil, "handshake timed out")
		if errors.Is(err, io.EOF) {
			return nil, errHelloBadFrame
		}
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return nil, errHelloTimeout
		}
		return nil, errHelloBadFrame
	}
	// Clear the read deadline: the jrpc2 channel owns the socket from
	// here and a lingering deadline would kill long subscribes.
	if err := raw.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear read deadline: %w", err)
	}

	var req helloRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeHelloError(raw, nil, "malformed hello frame")
		return nil, errHelloBadFrame
	}
	if req.JSONRPC != "2.0" || req.Method != "system.hello" {
		writeHelloError(raw, req.ID, "first frame must be system.hello")
		return nil, errHelloBadFrame
	}
	if req.Params.ProtoVersion != domain.ProtoVersion {
		writeHelloError(raw, req.ID,
			fmt.Sprintf("protoVersion %d not supported; server speaks %d", req.Params.ProtoVersion, domain.ProtoVersion))
		return nil, errHelloBadFrame
	}

	if err := writeHelloResult(raw, req.ID, serverVersion); err != nil {
		return nil, fmt.Errorf("write hello result: %w", err)
	}
	return br, nil
}

// writeHelloResult emits the success response on the socket. Frame is
// newline-terminated to match the line-delimited framing jrpc2 will
// use for the remainder of the connection.
func writeHelloResult(raw *net.UnixConn, id json.RawMessage, serverVersion string) error {
	// Defensive guard: even when the ldflag-injected version is dirty,
	// refuse to leak anything but pure semver on the wire.
	sv := sanitizeServerVersion(serverVersion)
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      helloID(id),
		"result": helloResult{
			ServerVersion:        sv,
			AcceptedProtoVersion: domain.ProtoVersion,
		},
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = raw.Write(data)
	return err
}

// writeHelloError emits a -32000 protocol_mismatch error frame. Best
// effort — failure to write is reported only to the server log.
func writeHelloError(raw *net.UnixConn, id json.RawMessage, detail string) {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      helloID(id),
		"error": map[string]any{
			"code":    int(CodeProtocolMismatch),
			"message": "protocol_mismatch",
			"data":    map[string]any{"reason": detail},
		},
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = raw.Write(data)
}

// helloID normalises the hello-request id so the response frame always
// round-trips a valid JSON-RPC id. A missing id becomes null.
func helloID(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	// Preserve the raw encoding so numeric ids stay numeric.
	return raw
}

// sanitizeServerVersion strips any non-semver suffix from the raw
// version string. Values that are not semver are reported as "0.0.0"
// so the wire field stays parseable — operators reading the log still
// see the raw build value under the serverVersion.raw slog attribute.
func sanitizeServerVersion(v string) string {
	v = stripVPrefix(v)
	if semverRE.MatchString(v) {
		return v
	}
	return "0.0.0"
}

func stripVPrefix(v string) string {
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		return v[1:]
	}
	return v
}
