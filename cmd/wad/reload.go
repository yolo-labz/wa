package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// handleReloadCommand dispatches `wad reload` and exits the process when
// handled. It dials the live daemon's unix socket for the resolved
// profile and invokes the admin.reload JSON-RPC method, which triggers
// the same code path as SIGHUP: atomic re-read of allowlist.toml, swap
// of in-memory state, and AuditReload emission. (FR-038)
func handleReloadCommand() (handled bool) {
	if len(os.Args) < 2 || os.Args[1] != "reload" {
		return false
	}
	os.Exit(runReload())
	return true
}

func runReload() int {
	profile := parseServiceProfileFlag()
	resolver, err := NewPathResolver(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad reload: profile %q: %v\n", profile, err)
		return 78
	}
	sockPath, err := resolver.SocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad reload: socket path: %v\n", err)
		return 78
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad reload: dial %s: %v\n", sockPath, err)
		return 10
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "admin.reload",
		"params":  struct{}{},
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		fmt.Fprintf(os.Stderr, "wad reload: encode: %v\n", err)
		return 1
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 64*1024), 1<<20)
	if !scan.Scan() {
		if err := scan.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "wad reload: read: %v\n", err)
		}
		return 1
	}

	var resp struct {
		Result struct {
			Schema  string `json:"schema"`
			Entries int    `json:"entries"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(scan.Bytes(), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "wad reload: parse: %v\n", err)
		return 1
	}
	if resp.Error != nil {
		fmt.Fprintf(os.Stderr, "wad reload: %d %s\n", resp.Error.Code, resp.Error.Message)
		return 1
	}

	if hasBoolFlag("--json") {
		_ = json.NewEncoder(os.Stdout).Encode(resp.Result)
	} else {
		fmt.Printf("allowlist reloaded (%d entries)\n", resp.Result.Entries)
	}
	return 0
}
