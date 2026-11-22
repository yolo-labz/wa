package admin

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestManualDocumentsAdminSurface keeps docs/manual.md honest: every route
// the admin mux registers (except the stylesheet) and every audit action the
// UI writes must appear in the manual's Admin UI section.
func TestManualDocumentsAdminSurface(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/manual.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "### Admin UI\n")
	end := strings.Index(doc, "### `wa wait`")
	if start < 0 || end < start {
		t.Fatal("manual has no '### Admin UI' section before '### `wa wait`'")
	}
	section := doc[start:end]

	routeRE := regexp.MustCompile(`HandleFunc\("(?:GET|POST) (/admin[^"]*)"`)
	files, _ := os.ReadDir(".")
	seen := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range routeRE.FindAllStringSubmatch(string(src), -1) {
			path := m[1]
			if path == "/admin/static/admin.css" || strings.Contains(path, "{nonce}") {
				continue // the stylesheet and the QR image are not operator pages
			}
			seen++
			if !strings.Contains(section, path) {
				t.Errorf("manual's Admin UI section never names %s", path)
			}
		}
	}
	if seen < 10 {
		t.Fatalf("found only %d admin routes in the package sources; the route scan is broken", seen)
	}

	for _, a := range []domain.AuditAction{
		domain.AuditAdminLogin, domain.AuditAdminLogout, domain.AuditTokenIssue, domain.AuditTokenRevoke,
		domain.AuditGrant, domain.AuditRevoke, domain.AuditPairStart, domain.AuditPairCancel,
		domain.AuditPairSuccessAfterCancel, domain.AuditUnlink,
	} {
		if !strings.Contains(section, "`"+a.String()+"`") {
			t.Errorf("manual's Admin UI section never names audit action %q", a.String())
		}
	}
}
