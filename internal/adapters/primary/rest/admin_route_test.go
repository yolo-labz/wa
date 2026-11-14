package rest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestAdmin_NotMountedOnRESTMux pins spec 116 SR-116-02: the admin UI
// lives only on its own loopback listener, so every /admin path on the
// REST listener is the mux's ordinary 404, authenticated or not.
func TestAdmin_NotMountedOnRESTMux(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef"
	srv := newServerForTest(t, &fakeDispatcher{}, token)
	base := "http://" + srv.ListenerAddr().String()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, path := range []string{"/admin", "/admin/", "/admin/login", "/admin/logout", "/admin/static/admin.css"} {
			for _, auth := range []string{"", "Bearer " + token} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				req, _ := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader("token="+token))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				resp, err := http.DefaultClient.Do(req)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("%s %s (auth=%t) on the REST listener = %d, want 404", method, path, auth != "", resp.StatusCode)
				}
			}
		}
	}
}

// TestWriteTimeout_BracketsSendReturnDeadline (WA-S4C1-SEND-DEADLINE, p7's
// nginx finding): both apps sit behind nginx with a 60 s proxy read/send
// timeout, and the REST server's own 30 s WriteTimeout used to drop every
// answer after 30 s (conn closed, nginx 502, though the send went through).
// The send deadline must fit nginx with margin (<= 45 s) and the write
// timeout must outlive it by >= 15 s, so neither can be changed alone.
func TestWriteTimeout_BracketsSendReturnDeadline(t *testing.T) {
	srv := newServerForTest(t, &fakeDispatcher{}, "0123456789abcdef0123456789abcdef0123456789abcdef")
	if app.SendReturnDeadline > 45*time.Second {
		t.Errorf("send return deadline %v > 45 s: no margin inside nginx's 60 s proxy timeout", app.SendReturnDeadline)
	}
	if got := srv.srv.WriteTimeout - app.SendReturnDeadline; got < 15*time.Second {
		t.Errorf("WriteTimeout %v outlives the send deadline %v by %v, want >= 15 s", srv.srv.WriteTimeout, app.SendReturnDeadline, got)
	}
}
