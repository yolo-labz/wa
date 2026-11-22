package admin

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
)

// TestValidateAddr (SR-116-02): only a loopback IP literal with an explicit
// port that no other listener uses may host the admin UI.
func TestValidateAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8080", "127.5.5.5:1", "[::1]:65535"} {
		if err := ValidateAddr(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for addr, want := range map[string]string{
		"127.0.0.1":       "admin addr",
		"localhost:8080":  "loopback IP literal",
		"0.0.0.0:8080":    "loopback IP literal",
		"100.64.0.1:80":   "loopback IP literal",
		"127.0.0.1:0":     "explicit port",
		"127.0.0.1:65536": "explicit port",
		"127.0.0.1:http":  "explicit port",
	} {
		if err := ValidateAddr(addr); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q = %v, want an error naming %q", addr, err, want)
		}
	}
	err := ValidateAddr("127.0.0.1:8080", "garbage", ":", "0.0.0.0:9090", "0.0.0.0:8080")
	if err == nil || !strings.Contains(err.Error(), "already used") || !strings.Contains(err.Error(), "0.0.0.0:8080") {
		t.Errorf("shared port = %v, want a clash with 0.0.0.0:8080", err)
	}
	if err := ValidateAddr("127.0.0.1:8080", "garbage", "0.0.0.0:x", "0.0.0.0:9090"); err != nil {
		t.Errorf("unparsable or different other ports clash: %v", err)
	}
}

// TestRender_FailureIs500AndWritesNothingElse: a template that cannot run
// answers 500 "render failed" with no partial page, and logs the template.
func TestRender_FailureIs500AndWritesNothingElse(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	New(Config{Profile: "p"}).render(httptest.NewRecorder(), http.StatusOK, "no-such-template.html", nil) // a nil Log must not panic
	f.s.render(rec, http.StatusOK, "no-such-template.html", nil)
	if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != "render failed" {
		t.Fatalf("render failure = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(f.logs.String(), "no-such-template.html") {
		t.Errorf("render failure not logged with its template: %q", f.logs.String())
	}
}

// TestHome_StatusPanelAndFailureModes (FR-116-01): the home page shows the
// daemon's status; an error or a stuck source reads "Status unavailable.".
func TestHome_StatusPanelAndFailureModes(t *testing.T) {
	f := newFixture(t)
	sess, _ := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	home := func() string {
		rec := f.do(req{path: "/admin", cookies: []*http.Cookie{sess}})
		if rec.Code != http.StatusOK {
			t.Fatalf("/admin = %d", rec.Code)
		}
		return rec.Body.String()
	}

	if body := home(); !strings.Contains(body, "Status unavailable.") || strings.Contains(body, "/admin/pair") {
		t.Errorf("no status source, no pairer: %q", body)
	}

	since := time.Date(2026, 11, 14, 9, 5, 0, 0, time.Local)
	if unixTime(0) != "" {
		t.Error("unixTime(0) must be empty (never)")
	}
	f.s.cfg.Status = func(context.Context) (Status, error) {
		return Status{Health: "degraded", Reasons: []string{"storage_unwritable"}, Paired: true, Account: "•••4321", Device: 7, SessionSince: since.Unix()}, nil
	}
	f.s.cfg.Pairer = &fakePairer{}
	body := home()
	for _, want := range []string{"degraded", "storage_unwritable", "•••4321", "14/11/2026 09:05", "/admin/pair"} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q: %q", want, body)
		}
	}
	if strings.Contains(body, "Status unavailable.") {
		t.Error("a healthy source still reads as unavailable")
	}

	f.s.cfg.Status = func(context.Context) (Status, error) { return Status{}, errors.New("boom") }
	if body := home(); !strings.Contains(body, "Status unavailable.") || !strings.Contains(f.logs.String(), "admin status") {
		t.Errorf("status error: page %q, log %q", body, f.logs.String())
	}

	f.s.cfg.Status = func(ctx context.Context) (Status, error) { <-ctx.Done(); return Status{}, ctx.Err() }
	start := time.Now()
	if body := home(); !strings.Contains(body, "Status unavailable.") || time.Since(start) > 3*time.Second {
		t.Errorf("a stuck source must give up inside its budget (took %v): %q", time.Since(start), body)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// TestStart_ServeFailureIsLoggedAndShutdownStillClean: a listener that dies
// is reported as "admin serve", while an orderly Shutdown stays silent.
func TestStart_ServeFailureIsLoggedAndShutdownStillClean(t *testing.T) {
	buf := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(buf, nil))

	dead := New(Config{Profile: "p", Log: log})
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	dead.Start(ln)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), "admin serve") && time.Now().Before(deadline) {
		<-time.After(10 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "admin serve") {
		t.Fatal("a dead listener was not logged")
	}
	_ = dead.Shutdown(t.Context())

	buf2 := &lockedBuf{}
	live := New(Config{Profile: "p", Log: slog.New(slog.NewTextHandler(buf2, nil))})
	ln2, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	live.Start(ln2)
	if c, err := new(net.Dialer).DialContext(t.Context(), "tcp", ln2.Addr().String()); err == nil { // serving before the shutdown
		_ = c.Close()
	}
	_ = live.Shutdown(t.Context())
	<-time.After(300 * time.Millisecond) // the Serve goroutine reports after Shutdown returns
	if strings.Contains(buf2.String(), "admin serve") {
		t.Errorf("an orderly shutdown logged a serve error: %q", buf2.String())
	}
}

type stubTail struct {
	recs      []AuditRecord
	tailErr   error
	verifyErr error
}

func (s stubTail) Tail(int) ([]AuditRecord, error) { return s.recs, s.tailErr }
func (s stubTail) Verify() error                   { return s.verifyErr }

// TestAdminAudit_UnavailableUnreadableAndUnparsedBreak (FR-116-06): a daemon
// without a chained file says so, an unreadable log is a 500 without its cause,
// and an unparsed verifier error gives the generic banner.
func TestAdminAudit_UnavailableUnreadableAndUnparsedBreak(t *testing.T) {
	f := newFixture(t)
	sess, _ := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	get := func() (int, string) {
		rec := f.do(req{path: "/admin/audit", cookies: []*http.Cookie{sess}})
		return rec.Code, rec.Body.String()
	}

	if code, body := get(); code != http.StatusOK || !strings.Contains(body, "not an HMAC-chained file") || strings.Contains(body, "<tr><td>") {
		t.Errorf("no tail source = %d %q", code, body)
	}

	f.s.cfg.AuditTail = stubTail{tailErr: errors.New("open /var/secret/audit.log: permission denied")}
	code, body := get()
	if code != http.StatusInternalServerError || strings.Contains(body, "/var/secret") || !strings.Contains(f.logs.String(), "admin audit tail") {
		t.Errorf("unreadable log = %d %q (log %q)", code, body, f.logs.String())
	}

	jid := "5511987654321@s.whatsapp.net"
	f.s.cfg.AuditTail = stubTail{
		recs:      []AuditRecord{{Time: time.Now(), Actor: "wad " + jid, Action: "send", Subject: jid, Decision: "ok", Detail: "to " + jid}},
		verifyErr: errors.New("slogaudit verify: key file /home/op/audit.key unreadable"),
	}
	code, body = get()
	if code != http.StatusOK || !strings.Contains(body, "Audit chain could not be verified") || !strings.Contains(body, "<tr><td>") {
		t.Errorf("unparsed break = %d %q: want the generic banner and the rows", code, body)
	}
	if strings.Contains(body, "/home/op") || strings.Contains(body, "5511987654321") || !strings.Contains(body, "•••4321@s.whatsapp.net") {
		t.Errorf("banner or rows leak: %q", body)
	}
}
