package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

type s1HTTPProbe bool

func (p s1HTTPProbe) WebsocketConnected() bool { return bool(p) }
func healthContractServer(t *testing.T, view domain.StorageHealth, connected bool) (string, *atomic.Int64) {
	t.Helper()
	m := memory.New(nil)
	session, _ := domain.NewSession(domain.MustJID("15551234567@s.whatsapp.net"), 1, time.Now())
	if err := m.Save(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int64{}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: s1HTTPProbe(connected), StorageHealth: func() domain.StorageHealth { calls.Add(1); return view }, SoftStaleThresholdSec: 300})
	t.Cleanup(func() { _ = d.Close() })
	addr := pickFreePort(t)
	t.Setenv("WAD_HEALTH_HTTP_ADDR", addr)
	srv, err := startHealthHTTP(d.IsReady, discardLogger(), d.ReadinessReasons)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return "http://" + addr, calls
}

func TestHealthHTTP_StorageFailureLeavesLiveness200(t *testing.T) {
	base, calls := healthContractServer(t, domain.StorageHealth{Storage: "failed", PersistFailuresTotal: 1}, true)
	resp := httpGetCtx(t, base+"/healthz")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls.Load() != 0 {
		t.Fatalf("liveness touched storage: status=%d reads=%d", resp.StatusCode, calls.Load())
	}
	resp = httpGetCtx(t, base+"/readyz")
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "storage_unwritable\n" {
		t.Fatalf("ready response=%d %q", resp.StatusCode, body)
	}
}

func TestReadyz_BodyIsReasonOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		view      domain.StorageHealth
		connected bool
		code      int
		body      string
	}{
		{"unknown", domain.StorageHealth{Storage: "unknown"}, true, 503, "storage_unknown\n"},
		{"failed", domain.StorageHealth{Storage: "failed"}, true, 503, "storage_unwritable\n"},
		{"disconnected", domain.StorageHealth{Storage: "ok"}, false, 503, "disconnected\n"},
		{"lag", domain.StorageHealth{Storage: "ok", IngestLagS: 301}, true, 503, "ingest_stale\n"},
		{"warning_only", domain.StorageHealth{Storage: "ok", FreeBytes: 1, LastReceivedUnixS: time.Now().Unix() - 360}, true, 200, "ready\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, _ := healthContractServer(t, tc.view, tc.connected)
			resp := httpGetCtx(t, base+"/readyz")
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.code || string(body) != tc.body {
				t.Fatalf("response=%d %q, want=%d %q", resp.StatusCode, body, tc.code, tc.body)
			}
			for _, forbidden := range []string{"15551234567", "@", "messages.db", "local test fixture", "***"} {
				if strings.Contains(string(body), forbidden) {
					t.Fatalf("readiness leaked %q", forbidden)
				}
			}
		})
	}
}

func TestReadyz_SoftStaleStays200(t *testing.T) {
	base, _ := healthContractServer(t, domain.StorageHealth{Storage: "ok", FreeBytes: 1, LastReceivedUnixS: time.Now().Unix() - 360}, true)
	resp := httpGetCtx(t, base+"/readyz")
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ready\n" {
		t.Fatalf("warnings drained traffic: %d %q", resp.StatusCode, body)
	}
}

func TestHealthEnv_RejectsInvalidAndOverflow(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "bad", "18446744073709551615"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WAD_STORAGE_PROBE_SEC", value)
			if got := healthEnv("WAD_STORAGE_PROBE_SEC", 30); got != 30 {
				t.Fatalf("env=%q got=%d", value, got)
			}
		})
	}
	t.Setenv("WAD_STORAGE_PROBE_SEC", "12")
	if got := healthEnv("WAD_STORAGE_PROBE_SEC", 30); got != 12 {
		t.Fatal(got)
	}
}
