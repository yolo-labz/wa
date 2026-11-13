package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

var (
	csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	healthRow = regexp.MustCompile(`id="health">Health: <strong>(\w+)</strong>(.*?)</p>`)
	readyRow  = regexp.MustCompile(`id="ready">Ready \(/readyz\): <strong>(yes|no)</strong>(.*?)</p>`)
	codeTok   = regexp.MustCompile(`<code>([a-z_]+)</code>`)
)

// adminClient talks to the real admin listener. __Host- cookies are
// Secure, so they are carried by hand over loopback http.
type adminClient struct {
	base    string
	cookies []*http.Cookie
	c       *http.Client
}

func (a *adminClient) do(t *testing.T, method, path string, form url.Values) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var body io.Reader = http.NoBody
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", a.base)
	}
	for _, c := range a.cookies {
		req.AddCookie(c)
	}
	resp, err := a.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	a.cookies = append(a.cookies, resp.Cookies()...)
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// statusServers starts /readyz and the admin listener over ONE
// dispatcher, wired exactly as main.go wires them.
func statusServers(t *testing.T, paired bool, view domain.StorageHealth, connected bool) (readyz string, admin *adminClient) {
	t.Helper()
	m := memory.New(nil)
	if paired {
		session, _ := domain.NewSession(domain.MustJID("15551234567@s.whatsapp.net"), 7, time.Now().Add(-time.Hour))
		if err := m.Save(t.Context(), session); err != nil {
			t.Fatal(err)
		}
	}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: s1HTTPProbe(connected), StorageHealth: func() domain.StorageHealth { return view }, SoftStaleThresholdSec: 300})
	t.Cleanup(func() { _ = d.Close() })

	db := tokenDB(t)
	store, err := sqlitetokens.Open(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := store.Issue(t.Context(), "ops", sqlitetokens.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	healthAddr, adminAddr := pickFreePort(t), pickFreePort(t)
	t.Setenv("WAD_HEALTH_HTTP_ADDR", healthAddr)
	t.Setenv("WAD_ADMIN_ADDR", adminAddr)
	t.Setenv("WAD_REST_TOKEN_DB", db)
	t.Setenv("WAD_REST_HTTP_ADDR", "")
	hs, err := startHealthHTTP(d.IsReady, discardLogger(), d.ReadinessReasons)
	if err != nil {
		t.Fatal(err)
	}
	bound, shutdown := startAdminHTTP(t.Context(), "default", nopAudit{}, nil, adminStatus(d), discardLogger())
	if bound == nil {
		t.Fatal("admin listener did not start")
	}
	tr := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tr.CloseIdleConnections()
		if err := shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := hs.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	admin = &adminClient{base: "http://" + bound.String(), c: &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	_, page := admin.do(t, http.MethodGet, "/admin/login", nil)
	m1 := csrfField.FindStringSubmatch(page)
	if m1 == nil {
		t.Fatal("login page has no csrf field")
	}
	if code, _ := admin.do(t, http.MethodPost, "/admin/login", url.Values{"csrf": {m1[1]}, "token": {tok.Raw}}); code != http.StatusSeeOther {
		t.Fatalf("login = %d", code)
	}
	return "http://" + healthAddr + "/readyz", admin
}

func codes(s string) []string {
	ms := codeTok.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// TestAdmin_StatusMatchesReadyz walks the spec 116 §6 truth table: the
// page shows S1's health and reasons, and its readiness line is exactly
// what /readyz serves at the same moment.
func TestAdmin_StatusMatchesReadyz(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name      string
		paired    bool
		view      domain.StorageHealth
		connected bool
		health    string
		reasons   []string
	}{
		{"not_paired", false, domain.StorageHealth{Storage: "ok", FreeBytes: 1 << 40}, false, "down", []string{"not_paired"}},
		{"storage_unwritable", true, domain.StorageHealth{Storage: "failed", PersistFailuresTotal: 3, LastPersistFailureAt: "2026-11-09T01:00:00Z"}, true, "down", []string{"storage_unwritable"}},
		{"storage_unknown", true, domain.StorageHealth{Storage: "unknown"}, true, "down", []string{"storage_unknown"}},
		{"disconnected", true, domain.StorageHealth{Storage: "ok", FreeBytes: 1 << 40}, false, "degraded", []string{"disconnected"}},
		{"ingest_stale", true, domain.StorageHealth{Storage: "ok", FreeBytes: 1 << 40, IngestLagS: 301}, true, "degraded", []string{"ingest_stale"}},
		{"warnings_only", true, domain.StorageHealth{Storage: "ok", FreeBytes: 1, LastReceivedUnixS: now - 360}, true, "degraded", []string{"soft_stale", "storage_low"}},
		{"healthy", true, domain.StorageHealth{Storage: "ok", FreeBytes: 1 << 40, LastReceivedUnixS: now, LastReceivedAt: "2026-11-09T01:40:00Z"}, true, "healthy", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readyz, admin := statusServers(t, tc.paired, tc.view, tc.connected)
			code, page := admin.do(t, http.MethodGet, "/admin", nil)
			h, r := healthRow.FindStringSubmatch(page), readyRow.FindStringSubmatch(page)
			if code != http.StatusOK || h == nil || r == nil {
				t.Fatalf("GET /admin = %d without health/ready lines:\n%s", code, page)
			}
			if h[1] != tc.health || !slices.Equal(codes(h[2]), tc.reasons) {
				t.Errorf("page health = %s %v, want %s %v", h[1], codes(h[2]), tc.health, tc.reasons)
			}

			resp := httpGetCtx(t, readyz)
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var want []string
			if resp.StatusCode != http.StatusOK {
				want = strings.Fields(string(body))
			}
			if (r[1] == "yes") != (resp.StatusCode == http.StatusOK) || !slices.Equal(codes(r[2]), want) {
				t.Errorf("page ready = %s %v; /readyz = %d %q", r[1], codes(r[2]), resp.StatusCode, body)
			}
			if tc.paired && (!strings.Contains(page, "***4567") || strings.Contains(page, "15551234567") || !strings.Contains(page, "<td>7</td>")) {
				t.Error("paired page must show the account masked to its last four digits and the device slot")
			}
			if tc.name == "storage_unwritable" && !strings.Contains(page, "<td>3</td>") {
				t.Error("persist_failures_total missing from the page")
			}
		})
	}
}
