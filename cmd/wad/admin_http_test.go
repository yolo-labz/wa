package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startAdminForTest runs startAdminHTTP with the given env and returns
// the bound address (nil when refused) and the captured log.
func startAdminForTest(t *testing.T, env map[string]string) (net.Addr, string) {
	t.Helper()
	for _, k := range []string{"WAD_ADMIN_ADDR", "WAD_REST_TOKEN_DB", "WAD_REST_HTTP_ADDR", "WAD_HEALTH_HTTP_ADDR", "WAD_ADMIN_HTTPS_ORIGINS"} {
		t.Setenv(k, env[k])
	}
	var buf bytes.Buffer
	addr, shutdown := startAdminHTTP(t.Context(), "default", nopAudit{}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	return addr, buf.String()
}

func tokenDB(t *testing.T) string { return filepath.Join(t.TempDir(), "tokens.db") }

func TestAdminListener_DisabledByDefault(t *testing.T) {
	addr, log := startAdminForTest(t, map[string]string{"WAD_REST_TOKEN_DB": tokenDB(t)})
	if addr != nil || log != "" {
		t.Fatalf("WAD_ADMIN_ADDR unset must mean no listener and no log: %v %q", addr, log)
	}
	// Scoped token DB is mandatory: env single-token auth has no scopes.
	addr, log = startAdminForTest(t, map[string]string{"WAD_ADMIN_ADDR": pickFreePort(t)})
	if addr != nil || !strings.Contains(log, "WAD_REST_TOKEN_DB") {
		t.Fatalf("admin without the token DB must be refused: %v %q", addr, log)
	}
}

func TestAdminListener_RefusesNonLoopbackBind(t *testing.T) {
	for _, bad := range []string{
		"localhost:9311", "0.0.0.0:9311", "[::]:9311", ":9311", "192.168.1.10:9311", "10.1.2.3:9311",
		"100.64.0.1:9311", "[fd7a:115c:a1e0::1]:9311", "8.8.8.8:9311", "127.0.0.1", "127.0.0.1:0", "wa.example:9311",
	} {
		addr, log := startAdminForTest(t, map[string]string{"WAD_ADMIN_ADDR": bad, "WAD_REST_TOKEN_DB": tokenDB(t)})
		if addr != nil || !strings.Contains(log, "admin http refused") {
			t.Errorf("%s: listener %v, log %q; want refused", bad, addr, log)
		}
	}
	good := pickFreePort(t)
	addr, _ := startAdminForTest(t, map[string]string{"WAD_ADMIN_ADDR": good, "WAD_REST_TOKEN_DB": tokenDB(t)})
	if addr == nil || addr.String() != good {
		t.Fatalf("loopback %s refused: %v", good, addr)
	}
}

func TestAdminListener_RefusesPortCollision(t *testing.T) {
	for _, other := range []string{"WAD_REST_HTTP_ADDR", "WAD_HEALTH_HTTP_ADDR"} {
		addr, log := startAdminForTest(t, map[string]string{
			"WAD_ADMIN_ADDR": "127.0.0.1:8080", "WAD_REST_TOKEN_DB": tokenDB(t), other: "0.0.0.0:8080",
		})
		if addr != nil || !strings.Contains(log, "already used") {
			t.Errorf("same port as %s: listener %v, log %q", other, addr, log)
		}
	}
}

func TestAdminTunnel_BridgesToLoopbackOnly(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(c, c) // echo until the tunnel half-closes
		_ = c.Close()
	}()
	var out bytes.Buffer
	if err := adminTunnel(t.Context(), ln.Addr().String(), strings.NewReader("GET /admin HTTP/1.1\r\n\r\n"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "GET /admin HTTP/1.1\r\n\r\n" {
		t.Fatalf("tunnel did not bridge: %q", out.String())
	}
	for _, bad := range []string{"192.168.1.10:9311", "100.64.0.1:9311", "localhost:9311", ""} {
		if err := adminTunnel(t.Context(), bad, strings.NewReader(""), io.Discard); err == nil {
			t.Errorf("tunnel to %q must be refused", bad)
		}
	}
}

// TestAdminTunnelHelperEcho is the forwarder's per-connection command in
// TestAdminTunnel_LocalForwarderLoopbackOnly; it is a no-op otherwise.
func TestAdminTunnelHelperEcho(t *testing.T) {
	if os.Getenv("WAD_TUNNEL_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(os.Stdout, os.Stdin)
	os.Exit(0)
}

func TestAdminTunnel_LocalForwarderLoopbackOnly(t *testing.T) {
	for _, bad := range []string{"0.0.0.0:0", ":0", "[::]:0", "localhost:0", "192.168.1.10:0", "[::1]:0"} {
		if ln, err := listenForwarder(t.Context(), bad); err == nil {
			_ = ln.Close()
			t.Errorf("forwarder bound %q; want 127.0.0.1 only", bad)
		}
	}
	if !strings.Contains(adminTunnelUsage, "no token") || strings.Contains(adminTunnelUsage, "--token") {
		t.Error("the tunnel grammar must take no token")
	}
	ln, err := listenForwarder(t.Context(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("bound %v", ip)
	}
	t.Setenv("WAD_TUNNEL_HELPER", "1")
	served := make(chan error, 1)
	go func() {
		served <- serveForwarder(t.Context(), ln, []string{os.Args[0], "-test.run=^TestAdminTunnelHelperEcho$"})
	}()
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	_, _ = conn.Write([]byte("ping"))
	_ = conn.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(conn)
	_ = conn.Close()
	_ = ln.Close()
	<-served
	if string(got) != "ping" {
		t.Fatalf("forwarder did not bridge the command's stdio: %q", got)
	}
}
