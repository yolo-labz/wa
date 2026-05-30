// Package main is the wa CLI client.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// sendHello performs the FR-012 handshake the daemon enforces since
// v2.0.0: every connection's first frame must be a system.hello with
// protoVersion == domain.ProtoVersion, else the daemon rejects with
// -32008 protocol_mismatch. callAndClose must call this immediately
// after dial, before the user's method, or all CLI commands fail.
func sendHello(conn net.Conn) error {
	params, err := json.Marshal(struct {
		ProtoVersion int `json:"protoVersion"`
	}{ProtoVersion: domain.ProtoVersion})
	if err != nil {
		return fmt.Errorf("marshal hello params: %w", err)
	}
	if _, rpcErr, err := call(conn, "system.hello", json.RawMessage(params)); err != nil {
		return fmt.Errorf("system.hello: %w", err)
	} else if rpcErr != nil {
		return rpcErr
	}
	return nil
}

// sendHelloOn performs the FR-012 handshake over an existing connection using
// the supplied persistent reader. Unlike sendHello (which calls the one-shot
// call() with its own throwaway scanner), this keeps the entire connection
// lifecycle — handshake plus every subsequent request — reading through the
// same *bufio.Reader, so no response bytes can be stranded in a scanner that
// is discarded after the handshake. Used by the media.fetchBytes chunk loop.
func sendHelloOn(conn net.Conn, br *bufio.Reader) error {
	params, err := json.Marshal(struct {
		ProtoVersion int `json:"protoVersion"`
	}{ProtoVersion: domain.ProtoVersion})
	if err != nil {
		return fmt.Errorf("marshal hello params: %w", err)
	}
	if _, rpcErr, err := callOn(conn, br, "system.hello", json.RawMessage(params)); err != nil {
		return fmt.Errorf("system.hello: %w", err)
	} else if rpcErr != nil {
		return rpcErr
	}
	return nil
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the error object in a JSON-RPC response.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// nextID is a process-global monotonic counter for JSON-RPC request IDs.
var nextID atomic.Int64

// dial connects to the daemon's unix socket. Uses context.Background()
// because the wa CLI is a short-lived invocation; the dial isn't
// expected to be cancelled mid-call.
func dial(socketPath string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	return conn, nil
}

// call sends a single JSON-RPC request and reads one response line.
func call(conn net.Conn, method string, params any) (json.RawMessage, *rpcError, error) {
	// If params is raw []byte from json.Marshal, wrap as json.RawMessage
	// so the outer json.Marshal embeds it verbatim instead of base64-encoding.
	if b, ok := params.([]byte); ok {
		params = json.RawMessage(b)
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, nil, fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, nil, fmt.Errorf("read response: %w", err)
		}
		return nil, nil, errors.New("read response: connection closed")
	}

	var resp rpcResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, resp.Error, nil
	}
	return resp.Result, nil, nil
}

// callOn sends one JSON-RPC request on an already-dialled, already-handshaked
// connection and reads exactly one '\n'-delimited response frame from br. It
// exists for the media.fetchBytes chunk loop (cmd_media.go), which reuses a
// single connection across many requests and must read frames larger than the
// 64 KiB default bufio.Scanner token the one-shot call() uses — a base64-
// inflated media chunk is ~683 KiB. br MUST be the only reader over conn so no
// pipelined response bytes are lost between successive calls.
//
// Returns (result, rpcError, transportError): a non-nil rpcError is a
// well-formed JSON-RPC error from the daemon; a non-nil transportError is a
// connection/parse failure.
func callOn(conn net.Conn, br *bufio.Reader, method string, params any) (json.RawMessage, *rpcError, error) {
	if b, ok := params.([]byte); ok {
		params = json.RawMessage(b)
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, nil, fmt.Errorf("write request: %w", err)
	}

	line, err := br.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error, nil
	}
	return resp.Result, nil, nil
}

// callAndClose dials, calls, closes, and maps errors to exit codes.
// Returns: result JSON, exit code, error (for transport failures).
//
// Spec 110c v0: when --remote is set globally, the call is routed
// over HTTP instead of the unix socket. socketPath is ignored in
// that mode. The token is read from the WA_TOKEN env var only —
// per Codex review §HIGH on PR #111, accepting it via --token would
// leak the secret through argv (visible to other users via /proc
// and ps).
func callAndClose(socketPath, method string, params any) (json.RawMessage, int, error) {
	if flagRemote != "" {
		return callRemote(flagRemote, os.Getenv("WA_TOKEN"), method, params)
	}

	conn, err := dial(socketPath)
	if err != nil {
		return nil, 10, err // service unavailable
	}
	defer func() { _ = conn.Close() }()

	if err := sendHello(conn); err != nil {
		return nil, 1, err
	}

	result, rpcErr, err := call(conn, method, params)
	if err != nil {
		return nil, 1, err
	}
	if rpcErr != nil {
		return nil, rpcCodeToExit(rpcErr.Code), rpcErr
	}
	return result, 0, nil
}
