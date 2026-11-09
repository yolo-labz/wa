package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// WA-ADMIN-SEC F5: at most maxTunnelConns children run at once; the rest
// are closed unanswered.
func TestAdminTunnel_ForwarderCapsConcurrency(t *testing.T) {
	ln, err := listenForwarder(t.Context(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAD_TUNNEL_HELPER", "1")
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- serveForwarder(ctx, ln, []string{os.Args[0], "-test.run=^TestAdminTunnelHelperEcho$"})
	}()
	const total = 3 * maxTunnelConns
	var d net.Dialer
	conns := make([]net.Conn, 0, total)
	for range total {
		c, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	refused := 0
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == io.EOF {
			refused++
		}
	}
	cancel()
	_ = ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	<-served
	if want := total - maxTunnelConns; refused != want {
		t.Fatalf("%d of %d connections refused, want %d (cap %d)", refused, total, want, maxTunnelConns)
	}
}

// WA-ADMIN-SEC F6: the allow handler the admin UI drives must not log a
// full JID.
func TestAllowHandler_LogsMaskedJID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	al := domain.NewAllowlist()
	var mu sync.RWMutex
	allow := handleAllow(al, &mu, filepath.Join(t.TempDir(), "allowlist.toml"), nopAudit{}, log)
	const user = "5511987654321"
	for _, p := range []map[string]any{
		{"op": "add", "jid": user + "@s.whatsapp.net", "actions": []string{"send"}},
		{"op": "remove", "jid": user + "@s.whatsapp.net", "actions": []string{"send"}},
	} {
		raw, _ := json.Marshal(p)
		if _, err := allow(t.Context(), raw); err != nil {
			t.Fatal(err)
		}
	}
	out := buf.String()
	if !strings.Contains(out, "allowlist add") || !strings.Contains(out, "allowlist remove") {
		t.Fatalf("expected both log lines, got %q", out)
	}
	if strings.Contains(out, user) || strings.Contains(out, "55119876") {
		t.Fatalf("full JID reached the log: %q", out)
	}
}
