package admin

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// rawPost sends body with ctype to POST /admin/login carrying a valid
// pre-login cookie, the only thing an unauthenticated peer needs.
func (f *fixture) rawPost(t *testing.T, ctype string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	pre, _ := f.loginPage(t, "")
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/login", body)
	hr.Host, hr.RemoteAddr = testHost, "127.0.0.1:50000"
	hr.Header.Set("Content-Type", ctype)
	hr.Header.Set("Origin", testOrigin)
	hr.AddCookie(pre)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, hr)
	return rec
}

func TestAdmin_PostBodyCapped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := newFixture(t)
	pad := url.Values{"csrf": {"x"}, "token": {strings.Repeat("A", 1<<20)}}.Encode()
	if rec := f.rawPost(t, "application/x-www-form-urlencoded", strings.NewReader(pad)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("1 MiB urlencoded = %d, want 413", rec.Code)
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		w, err := mw.CreateFormFile("junk", "junk.bin")
		if err != nil {
			return
		}
		chunk := make([]byte, 1<<20)
		for range 256 {
			if _, err := w.Write(chunk); err != nil {
				break
			}
		}
		_ = mw.Close()
		_ = pw.CloseWithError(io.ErrClosedPipe)
	}()
	rec := f.rawPost(t, mw.FormDataContentType(), pr)
	_ = pr.Close()
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("256 MiB multipart = %d, want 413 or 415", rec.Code)
	}
	if ents, err := os.ReadDir(tmp); err != nil || len(ents) != 0 {
		t.Fatalf("TMPDIR holds %d entries after the refused bodies (err %v)", len(ents), err)
	}
}

func TestAdmin_MultipartRefused(t *testing.T) {
	f := newFixture(t)
	var b strings.Builder
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("token", "x")
	_ = mw.Close()
	if rec := f.rawPost(t, mw.FormDataContentType(), strings.NewReader(b.String())); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("small multipart = %d, want 415", rec.Code)
	}
	if rec := f.rawPost(t, "text/plain", strings.NewReader("token=x")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain = %d, want 415", rec.Code)
	}
}

func TestAdmin_LimiterAtomicUnderParallelLogins(t *testing.T) {
	f := newFixture(t)
	const n = 50
	type pair struct {
		pre  *http.Cookie
		csrf string
	}
	pairs := make([]pair, n)
	for i := range pairs {
		pairs[i].pre, pairs[i].csrf = f.loginPage(t, "")
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			codes[i] = f.do(req{
				method: http.MethodPost, path: "/admin/login",
				form:    url.Values{"csrf": {pairs[i].csrf}, "token": {"wrong"}},
				cookies: []*http.Cookie{pairs[i].pre},
			}).Code
		})
	}
	wg.Wait()
	var denied, limited int
	for _, c := range codes {
		switch c {
		case http.StatusUnauthorized:
			denied++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if denied != loginLimit || limited != n-loginLimit {
		t.Fatalf("%d parallel bad logins: %d x 401, %d x 429; want exactly %d x 401", n, denied, limited, loginLimit)
	}
	if got := len(f.audit.decisions(domain.AuditAdminLogin)); got > 2*loginLimit {
		t.Fatalf("%d login audit rows for %d verified attempts", got, loginLimit)
	}
}
