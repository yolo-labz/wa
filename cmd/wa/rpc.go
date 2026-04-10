// Package main is the wa CLI client.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
)

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

// dial connects to the daemon's unix socket.
func dial(socketPath string) (net.Conn, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	return conn, nil
}

// call sends a single JSON-RPC request and reads one response line.
func call(conn net.Conn, method string, params any) (json.RawMessage, *rpcError, error) {
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
		return nil, nil, fmt.Errorf("read response: connection closed")
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

// callAndClose dials, calls, closes, and maps errors to exit codes.
// Returns: result JSON, exit code, error (for transport failures).
func callAndClose(socketPath, method string, params any) (json.RawMessage, int, error) {
	conn, err := dial(socketPath)
	if err != nil {
		return nil, 10, err // service unavailable
	}
	defer func() { _ = conn.Close() }()

	result, rpcErr, err := call(conn, method, params)
	if err != nil {
		return nil, 1, err
	}
	if rpcErr != nil {
		return nil, rpcCodeToExit(rpcErr.Code), rpcErr
	}
	return result, 0, nil
}
