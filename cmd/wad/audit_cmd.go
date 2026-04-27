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

// handleAuditCommand dispatches `wad audit …` subcommands. Only the
// rotate subcommand exists today; others return EX_USAGE.
func handleAuditCommand() (handled bool) {
	if len(os.Args) < 2 || os.Args[1] != "audit" {
		return false
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: wad audit rotate [--profile <name>] [--json]")
		os.Exit(64)
	}
	switch os.Args[2] {
	case "rotate":
		os.Exit(runAuditRotate())
	default:
		fmt.Fprintf(os.Stderr, "wad audit: unknown subcommand %q\n", os.Args[2])
		os.Exit(64)
	}
	return true
}

// runAuditRotate dials the live daemon and invokes admin.audit.rotate.
func runAuditRotate() int {
	profile := parseServiceProfileFlag()
	resolver, err := NewPathResolver(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: profile %q: %v\n", profile, err)
		return 78
	}
	sockPath, err := resolver.SocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: socket path: %v\n", err)
		return 78
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: dial %s: %v\n", sockPath, err)
		return 10
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "admin.audit.rotate",
		"params":  struct{}{},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: encode: %v\n", err)
		return 1
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 64*1024), 1<<20)
	if !scan.Scan() {
		if err := scan.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "wad audit rotate: read: %v\n", err)
		}
		return 1
	}

	var resp struct {
		Result auditRotateResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(scan.Bytes(), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: parse: %v\n", err)
		return 1
	}
	if resp.Error != nil {
		fmt.Fprintf(os.Stderr, "wad audit rotate: %d %s\n", resp.Error.Code, resp.Error.Message)
		return 1
	}

	if hasBoolFlag("--json") {
		_ = json.NewEncoder(os.Stdout).Encode(resp.Result)
	} else {
		fmt.Printf("rotated: %s (%d bytes)\nactive:  %s\n",
			resp.Result.RotatedPath, resp.Result.RotatedBytes, resp.Result.NewPath)
	}
	return 0
}
