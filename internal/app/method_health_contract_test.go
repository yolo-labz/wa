package app_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func newHealthFixture(t *testing.T, paired, connected bool, view domain.StorageHealth) *app.Dispatcher {
	t.Helper()
	m := memory.New(nil)
	if paired {
		session, err := domain.NewSession(domain.MustJID(testJIDStr), 1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = m.Save(t.Context(), session); err != nil {
			t.Fatal(err)
		}
	}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: &fakeProbe{up: connected}, StorageHealth: func() domain.StorageHealth { return view }, SoftStaleThresholdSec: 300})
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func healthDocument(t *testing.T, d *app.Dispatcher, method string) (map[string]any, string) {
	t.Helper()
	raw, err := d.Handle(t.Context(), method, nil)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v, string(raw)
}

func TestHealthEndpointsTruthTable(t *testing.T) {
	for _, storage := range []string{"unknown", "failed", "ok"} {
		for flags := range 32 {
			t.Run(fmt.Sprintf("%s/%05b", storage, flags), func(t *testing.T) {
				paired, connected, ingest, soft, low := flags&1 != 0, flags&2 != 0, flags&4 != 0, flags&8 != 0, flags&16 != 0
				view := domain.StorageHealth{Storage: storage, FreeBytes: 128 << 20}
				if ingest {
					view.IngestLagS = 301
				}
				if soft {
					view.LastReceivedUnixS = time.Now().Unix() - 301
				}
				if low {
					view.FreeBytes = 1
				}
				d := newHealthFixture(t, paired, connected, view)
				wantReasons := []string{}
				if !paired {
					wantReasons = append(wantReasons, "not_paired")
				}
				if storage == "unknown" {
					wantReasons = append(wantReasons, "storage_unknown")
				}
				if storage == "failed" {
					wantReasons = append(wantReasons, "storage_unwritable")
				}
				down := len(wantReasons) > 0
				if paired && !connected {
					wantReasons = append(wantReasons, "disconnected")
				}
				if ingest && paired && connected {
					wantReasons = append(wantReasons, "ingest_stale")
				}
				unready := len(wantReasons) > 0
				if soft && paired && connected {
					wantReasons = append(wantReasons, "soft_stale")
				}
				if low && storage == "ok" {
					wantReasons = append(wantReasons, "storage_low")
				}
				wantHealth := "healthy"
				if len(wantReasons) > 0 {
					wantHealth = "degraded"
				}
				if down {
					wantHealth = "down"
				}
				for _, method := range []string{"health", "status"} {
					v, raw := healthDocument(t, d, method)
					if v["health"] != wantHealth || v["connected"] != (paired && connected) {
						t.Fatalf("wrong truth table: %s", raw)
					}
					got := []string{}
					if list, ok := v["reasons"].([]any); ok {
						for _, r := range list {
							got = append(got, r.(string))
						}
					}
					if !reflect.DeepEqual(got, wantReasons) {
						t.Fatalf("reasons=%v want=%v", got, wantReasons)
					}
				}
				if d.IsReady(t.Context()) == unready {
					t.Fatalf("readiness contradicted table; reasons=%v", d.ReadinessReasons(t.Context()))
				}
			})
		}
	}
}

func TestHealthReadDoesNotProbeDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A blocked worker cannot block the cached callback used by any reader.
		release := make(chan struct{})
		done := make(chan struct{})
		go func() { defer close(done); <-release }()
		d := newHealthFixture(t, true, true, domain.StorageHealth{Storage: "ok", FreeBytes: 128 << 20})
		synctest.Wait()
		start := time.Now()
		for range 100 {
			healthDocument(t, d, "health")
			healthDocument(t, d, "status")
			if !d.IsReady(t.Context()) {
				t.Fatal("cached readiness failed")
			}
		}
		if elapsed := time.Since(start); elapsed >= time.Millisecond {
			t.Fatalf("reader waited for probe: %v", elapsed)
		}
		close(release)
		<-done
	})
}

func TestStatus_AccountMaskedToLastFour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newHealthFixture(t, true, true, domain.StorageHealth{Storage: "ok", FreeBytes: 128 << 20})
		v, raw := healthDocument(t, d, "status")
		if v["jid"] != "***9999" || strings.Contains(raw, testJIDStr) || strings.Contains(raw, "5511999999999") {
			t.Fatalf("unmasked account: %s", raw)
		}
		if _, ok := v["schema"]; ok {
			t.Fatal("status unexpectedly added an empty health schema")
		}
	})
}

func TestStatus_ConnectedIsLive(t *testing.T) {
	for _, connected := range []bool{false, true} {
		d := newHealthFixture(t, true, connected, domain.StorageHealth{Storage: "ok", FreeBytes: 128 << 20})
		v, _ := healthDocument(t, d, "status")
		if v["connected"] != connected || d.IsReady(t.Context()) != connected {
			t.Fatalf("status is not live: %v", v)
		}
	}
	m := memory.New(nil)
	session, _ := domain.NewSession(domain.MustJID(testJIDStr), 1, time.Now())
	_ = m.Save(t.Context(), session)
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, StorageHealth: func() domain.StorageHealth { return domain.StorageHealth{Storage: "ok"} }})
	t.Cleanup(func() { _ = d.Close() })
	for _, method := range []string{"status", "health"} {
		v, _ := healthDocument(t, d, method)
		if v["connected"] != false || d.IsReady(t.Context()) {
			t.Fatalf("%s invented connectivity", method)
		}
	}
}
