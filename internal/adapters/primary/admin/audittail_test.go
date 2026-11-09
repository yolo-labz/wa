package admin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fileTail is the test twin of cmd/wad's adminAuditShim.
type fileTail struct{ a *slogaudit.Audit }

func (s fileTail) Tail(n int) ([]AuditRecord, error) {
	es, err := s.a.Tail(n)
	out := make([]AuditRecord, 0, len(es))
	for _, e := range es {
		out = append(out, AuditRecord{Time: e.EventTime, Actor: e.Actor, Action: e.Action, Subject: e.Subject, Decision: e.Decision, Detail: e.Detail, Source: e.Source})
	}
	return out, err
}

func (s fileTail) Verify() error { _, err := s.a.Verify(); return err }

// auditFixture is a signed-in admin whose audit log is a real chained
// file holding n synthetic events.
func auditFixture(t *testing.T, n int) (*fixture, *http.Cookie, string) {
	t.Helper()
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	jid, _ := domain.Parse("5511987654321@s.whatsapp.net")
	base := time.Now().Add(-time.Hour)
	for i := range n {
		e := domain.NewAuditEventFrom("socket", "wa send", domain.AuditSend, jid, "ok", "seq="+strconv.Itoa(i)+" to 5511987654321@s.whatsapp.net")
		e.TS = base.Add(time.Duration(i) * time.Millisecond)
		if err := a.Record(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	f.s.cfg.AuditTail = fileTail{a}
	sess, _ := f.login(t, f.issue(t, "ops", sqlitetokens.ScopeAdmin, 0).Raw)
	return f, sess, path
}

func TestAdminAudit_TailVerifiesChain(t *testing.T) {
	f, sess, _ := auditFixture(t, 250)
	rec := f.do(req{path: "/admin/audit", cookies: []*http.Cookie{sess}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "chain broken") {
		t.Fatalf("GET /admin/audit = %d, broken banner on an intact chain: %t", rec.Code, strings.Contains(body, "chain broken"))
	}
	if got := strings.Count(body, "<tr><td>"); got != 200 {
		t.Fatalf("rows = %d, want the last 200", got)
	}
	// Newest first: seq=249 appears, and before seq=50; seq=49 is past the window.
	i249, i50 := strings.Index(body, "seq=249 "), strings.Index(body, "seq=50 ")
	if i249 < 0 || i50 < 0 || i249 > i50 || strings.Contains(body, "seq=49 ") {
		t.Fatalf("order/window wrong: seq=249@%d seq=50@%d", i249, i50)
	}
	if strings.Contains(body, "5511987654321") || !strings.Contains(body, "•••4321@s.whatsapp.net") {
		t.Fatal("JIDs must be masked in subject and detail")
	}
}

func TestAdminAudit_BrokenChainBanner(t *testing.T) {
	f, sess, path := auditFixture(t, 5)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"decision":"ok"`, `"decision":"no"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	body := f.do(req{path: "/admin/audit", cookies: []*http.Cookie{sess}}).Body.String()
	if !strings.Contains(body, "Audit chain broken") || !strings.Contains(body, "line 1") {
		t.Fatalf("tampered chain must show a banner naming the line: %q", body)
	}
	if got := strings.Count(body, "<tr><td>"); got != 5 {
		t.Fatalf("rows = %d; a broken chain is a banner, not missing rows", got)
	}
}
