package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/admin"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const throughPairCode = "2@through-pair,ref,keys"

// sinkPairer is the daemon's app.Pairer (Adapter.Pair's port): it plays one
// code into the ctx's QR sink, then pairs until its ctx ends or the test
// hands it a result.
type sinkPairer struct {
	ran    chan context.Context
	result chan error
}

func (p *sinkPairer) Pair(ctx context.Context, _ string) error {
	if sink := app.QRSinkFrom(ctx); sink != nil {
		sink(throughPairCode)
	}
	p.ran <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-p.result:
		return err
	}
}

var qrImgRE = regexp.MustCompile(`src="(/admin/pair/[^"]+/qr\.png)"`)

type throughPair struct {
	h     http.Handler
	srv   *admin.Server
	p     *sinkPairer
	store *sqlitetokens.Store
	tok   sqlitetokens.Token
	sess  *http.Cookie
	csrf  string
}

// newThroughPair wires the admin UI to the production dispatch boundary:
// admin routes → adminPairer → Handle/Executor.Run as the admin token's
// principal → dispatcher pair → the app.Pairer port.
func newThroughPair(t *testing.T) *throughPair {
	t.Helper()
	x := &throughPair{p: &sinkPairer{ran: make(chan context.Context, 2), result: make(chan error, 1)}}
	da, _ := jobsAdapter(t, x.p, nil)
	var err error
	if x.store, err = sqlitetokens.Open(t.Context(), filepath.Join(t.TempDir(), "tokens.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.store.Close() })
	da.tokens.Store(x.store)
	if x.tok, err = x.store.Issue(t.Context(), "ops", sqlitetokens.ScopeAdmin, 0); err != nil {
		t.Fatal(err)
	}
	x.srv = admin.New(admin.Config{
		Profile: "default", Tokens: adminTokenShim{x.store}, Audit: nopAudit{}, RPC: da,
		Pairer: adminPairer{da: da, session: memory.New(nil)},
	})
	t.Cleanup(func() { _ = x.srv.Shutdown(context.Background()); synctest.Wait() })
	x.h = x.srv.Handler()
	page := adminGet(t, x.h, "/admin/login")
	rec := adminPost(t, x.h, "/admin/login", url.Values{"csrf": {csrfOf(t, page)}, "token": {x.tok.Raw}}, page.Result().Cookies()...)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-wa_admin" {
			x.sess = c
		}
	}
	if x.sess == nil {
		t.Fatalf("login = %d", rec.Code)
	}
	x.csrf = csrfOf(t, adminGet(t, x.h, "/admin/pair", x.sess))
	return x
}

// start begins a pairing and returns the pair job's ctx once Pair runs.
func (x *throughPair) start(t *testing.T) context.Context {
	t.Helper()
	if rec := adminPost(t, x.h, "/admin/pair/start", url.Values{"csrf": {x.csrf}}, x.sess); rec.Code != http.StatusSeeOther {
		t.Fatalf("pair start = %d %q", rec.Code, rec.Body.String())
	}
	return <-x.p.ran
}

// TestAdminPair_ThroughPairJob (ruling D2): the admin UI's pairing runs
// the daemon's pair method as a job owned by the admin token: the flow's
// QR sink reaches the Pairer port, the code shows as a same-origin PNG,
// and an admin cancel ends the job's ctx.
func TestAdminPair_ThroughPairJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := newThroughPair(t)
		job := x.start(t)
		synctest.Wait()
		m := qrImgRE.FindStringSubmatch(adminGet(t, x.h, "/admin/pair", x.sess).Body.String())
		if m == nil {
			t.Fatal("the code played into the job ctx's sink did not reach the pair page")
		}
		if rec := adminGet(t, x.h, m[1], x.sess); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("QR fetch = %d %q", rec.Code, rec.Header().Get("Content-Type"))
		}
		adminPost(t, x.h, "/admin/pair/cancel", url.Values{"csrf": {x.csrf}}, x.sess)
		<-time.After(2 * time.Second)
		synctest.Wait()
		if !errors.Is(job.Err(), context.Canceled) {
			t.Fatalf("admin cancel did not reach the pair job: %v", job.Err())
		}
	})
}

// TestAdminPair_ThroughPairJobOwnedByToken: the job's owner is the admin
// token, so revoking it in the store cancels the pairing within one owner
// sweep, and a job's success ends the flow as paired.
func TestAdminPair_ThroughPairJobOwnedByToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := newThroughPair(t)
		job := x.start(t)
		if err := x.store.Revoke(t.Context(), x.tok.ID); err != nil {
			t.Fatal(err)
		}
		<-time.After(31 * time.Second)
		synctest.Wait()
		if job.Err() == nil {
			t.Fatal("revoking the owning token did not end the pair job")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		x := newThroughPair(t)
		x.start(t)
		x.p.result <- nil
		<-time.After(2 * time.Second)
		synctest.Wait()
		body := adminGet(t, x.h, "/admin/pair", x.sess).Body.String()
		if !regexp.MustCompile(`last pairing ended: paired`).MatchString(body) {
			t.Fatalf("a finished pair job did not end the flow as paired: %q", body)
		}
	})
}

// TestWatchPairSuccess: only PairSuccess on the daemon's event stream
// reaches the admin's success observer.
func TestWatchPairSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan app.Event)
		ctx, cancel := context.WithCancel(t.Context())
		n := 0
		done := make(chan struct{})
		go func() { defer close(done); watchPairSuccess(ctx, events, func() { n++ }) }()
		for _, st := range []domain.PairingState{domain.PairFailure, domain.PairSuccess, domain.PairQRCode} {
			events <- app.Event{Type: "pairing", Payload: domain.PairingEvent{State: st}}
		}
		synctest.Wait()
		cancel()
		<-done
		if n != 1 {
			t.Errorf("observer called %d times, want 1", n)
		}
	})
}

// pairEvents is a one-subscriber event source.
type pairEvents struct {
	filter    []string
	cancelled bool
}

func (e *pairEvents) SubscribeStream(filter []string, _ int) (<-chan app.Event, func()) {
	e.filter = filter
	return make(chan app.Event), func() { e.cancelled = true }
}

// TestAdminListener_PairWiring: the daemon's admin listener mounts the
// pairing routes only with a Pairer, and watches pairing events until
// shutdown.
func TestAdminListener_PairWiring(t *testing.T) {
	for _, withPair := range []bool{false, true} {
		t.Setenv("WAD_ADMIN_ADDR", pickFreePort(t))
		t.Setenv("WAD_REST_TOKEN_DB", tokenDB(t))
		var pair admin.Pairer
		if withPair {
			pair = adminPairer{session: memory.New(nil)}
		}
		ev := &pairEvents{}
		addr, shutdown := startAdminHTTP(t.Context(), "default", nopAudit{}, nil, nil, pair, ev, slog.New(slog.DiscardHandler))
		if addr == nil {
			t.Fatal("admin listener refused")
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr.String()+"/admin/pair", http.NoBody)
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if (resp.StatusCode != http.StatusNotFound) != withPair {
			t.Errorf("pair=%t: GET /admin/pair = %d", withPair, resp.StatusCode)
		}
		if withPair != (len(ev.filter) == 1 && ev.filter[0] == "pairing") {
			t.Errorf("pair=%t: subscribed to %v", withPair, ev.filter)
		}
		if err := shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if ev.cancelled != withPair {
			t.Errorf("pair=%t: event subscription cancelled = %t", withPair, ev.cancelled)
		}
	}
}
