package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// jobsAdapter is the production dispatch boundary (dispatcherAdapter and
// its executor) over a memory dispatcher; pairer nil uses the memory one.
// stop is the daemon's shutdown of it, run at cleanup if not before.
func jobsAdapter(t *testing.T, pairer app.Pairer, intercepts map[string]compositionHandler) (da *dispatcherAdapter, stop func()) {
	t.Helper()
	mem := memory.New(nil)
	if pairer == nil {
		pairer = mem
	}
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: mem, Events: mem, Contacts: mem, Groups: mem, Session: mem,
		Allowlist: mem, Audit: mem, History: mem, Pairer: pairer,
		SessionCreated: time.Now(), Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithCancel(context.Background())
	da = newDispatcherAdapter(ctx, d, intercepts)
	stop = sync.OnceFunc(func() {
		cancel()
		da.Close()
		_ = d.Close()
	})
	t.Cleanup(stop)
	return da, stop
}

type handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

func mustRPC(t *testing.T, h handler, method, params string) map[string]any {
	t.Helper()
	raw, err := h.Handle(t.Context(), method, json.RawMessage(params))
	if err != nil {
		t.Fatalf("%s %s: %v", method, params, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: decode %s: %v", method, raw, err)
	}
	return out
}

func jobArg(id any) string { return `{"job_id":"` + id.(string) + `"}` }

// blockingPairer pairs until its context ends and reports why, and
// whether that context carried a QR sink.
type blockingPairer struct {
	started chan bool
	stopped chan error
}

func (p *blockingPairer) Pair(ctx context.Context, _ string) error {
	p.started <- app.QRSinkFrom(ctx) != nil
	<-ctx.Done()
	p.stopped <- ctx.Err()
	return ctx.Err()
}

// TestMethodDeadlines_DetachedInventoryAndCancellation: every handler
// that detached is inventoried as a job, no handler path builds its own
// root context outside the lifecycle roots, and jobs.cancel and shutdown
// reach a pairing worker (owner revocation: TestSyncJobs_CancelledOnTokenRevoke).
func TestMethodDeadlines_DetachedInventoryAndCancellation(t *testing.T) {
	for m, how := range app.DetachedHandlers {
		if dl := app.MethodDeadlines[m]; dl.Class != app.Job || dl.MaxWorker <= 0 || how == "" {
			t.Errorf("detached handler %q: deadline %+v, inventory %q; want a job with a worker lifetime", m, dl, how)
		}
	}
	// A context rooted at Background (or stripped of cancellation) on a
	// handler path escapes jobs.cancel, revocation and shutdown.
	roots := map[string]bool{ // lifecycle roots, not request work
		"internal/app/dispatcher.go": true, "internal/app/eventbridge.go": true, "internal/app/executor.go": true,
		"internal/adapters/secondary/whatsmeow/adapter.go": true, // client connection lifetime
	}
	detach := regexp.MustCompile(`context\.With(Timeout|Deadline|Cancel)\(context\.Background\(\)|context\.WithoutCancel\(`)
	for _, dir := range []string{"internal/app", "internal/adapters/secondary/whatsmeow"} {
		files, _ := filepath.Glob(filepath.Join("..", "..", dir, "*.go"))
		if len(files) == 0 {
			t.Fatalf("no sources under %s", dir)
		}
		for _, f := range files {
			rel := strings.TrimPrefix(filepath.ToSlash(f), "../../")
			if strings.HasSuffix(f, "_test.go") || roots[rel] {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if loc := detach.FindIndex(src); loc != nil {
				t.Errorf("%s:%d detaches a context; derive it from the caller's (worker) context or inventory it",
					rel, bytes.Count(src[:loc[0]], []byte("\n"))+1)
			}
		}
	}

	synctest.Test(t, func(t *testing.T) {
		p := &blockingPairer{started: make(chan bool, 2), stopped: make(chan error, 2)}
		da, shutdown := jobsAdapter(t, p, nil)
		h := socketCaller{da, "default"}

		// The admin flow sets its QR sink on the request ctx; it reaches
		// Pair through the job, whose cancellation is the job's own.
		raw, err := h.Handle(app.WithQRSink(t.Context(), func(string) {}), "pair", json.RawMessage(`{"async":true}`))
		var job map[string]any
		if err != nil || json.Unmarshal(raw, &job) != nil {
			t.Fatalf("pair: %s %v", raw, err)
		}
		if !<-p.started {
			t.Error("the request ctx's QR sink did not reach Pair")
		}
		if got := mustRPC(t, h, "jobs.cancel", jobArg(job["job_id"])); got["state"] == app.StateDone {
			t.Fatalf("cancel answered %v", got)
		}
		if err := <-p.stopped; !errors.Is(err, context.Canceled) {
			t.Fatalf("jobs.cancel did not reach the pairing worker: %v", err)
		}

		mustRPC(t, h, "pair", `{"async":true}`)
		<-p.started
		shutdown()
		if err := <-p.stopped; err == nil {
			t.Fatal("shutdown did not reach the pairing worker")
		}
	})
}

// TestMediaDownload_SyncModeDeadline: without async, a fast download keeps
// its synchronous shape and a slow one answers {job_id, running,
// timed_out} at exactly 10 s, then reports its one result through the job;
// async:true queues at once, and a failure arrives as the typed error row.
func TestMediaDownload_SyncModeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		download := func(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
			calls.Add(1)
			var p struct{ Slow, Fail bool }
			_ = json.Unmarshal(params, &p)
			if p.Slow {
				<-release // ignores cancellation, like a stuck download
			}
			if p.Fail {
				return nil, domain.ErrMediaNotCached
			}
			return json.RawMessage(`{"path":"/cache/a"}`), nil
		}
		da, _ := jobsAdapter(t, nil, map[string]compositionHandler{"media.download": download})
		h := socketCaller{da, "default"}

		if got := mustRPC(t, h, "media.download", `{}`); got["path"] != "/cache/a" || got["job_id"] != nil {
			t.Fatalf("fast download = %v, want the synchronous result", got)
		}

		start := time.Now()
		job := mustRPC(t, h, "media.download", `{"slow":true}`)
		if el := time.Since(start); el != app.ReturnDeadline || job["state"] != app.StateRunning || job["timed_out"] != true {
			t.Fatalf("slow download answered %v after %v, want running+timed_out at %v", job, el, app.ReturnDeadline)
		}
		if got := mustRPC(t, h, "jobs.get", jobArg(job["job_id"])); got["state"] != app.StateRunning {
			t.Fatalf("poll before completion = %v", got)
		}
		close(release)
		synctest.Wait()
		got := mustRPC(t, h, "jobs.get", jobArg(job["job_id"]))
		if res, _ := got["result"].(map[string]any); got["state"] != app.StateDone || res["path"] != "/cache/a" {
			t.Fatalf("finished job = %v", got)
		}

		start = time.Now()
		job = mustRPC(t, h, "media.download", `{"async":true,"fail":true}`)
		if time.Since(start) != 0 || job["state"] != app.StateQueued {
			t.Fatalf("async = %v, want queued at once", job)
		}
		synctest.Wait()
		got = mustRPC(t, h, "jobs.get", jobArg(job["job_id"]))
		e, _ := got["error"].(map[string]any)
		if got["state"] != app.StateFailed || e["code"] != float64(-32301) || e["reason"] != string(domain.ReasonMediaNotCached) {
			t.Fatalf("failed job = %v, want the media_not_cached row", got)
		}
		if n := calls.Load(); n != 3 {
			t.Fatalf("download ran %d times, want 3 (never re-run)", n)
		}
	})
}

// principalStore issues one token whose id imitates a socket principal.
type principalStore struct{ id string }

func (s principalStore) Verify(context.Context, string) (rest.Scope, string, error) {
	return rest.ScopeReadStr, s.id, nil
}

// callerLog records the principal each dispatch was attributed to.
type callerLog struct {
	mu   sync.Mutex
	seen []app.Principal
}

func (c *callerLog) Handle(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	p, ok := app.CallerFrom(ctx)
	if !ok {
		p = app.Principal{ID: "<unattributed>"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, p)
	return json.RawMessage(`{"connected":true}`), nil
}

func serveREST(t *testing.T, d rest.Dispatcher, auth rest.Authenticator) string {
	t.Helper()
	provider, err := buildMCPProvider(d, "test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rest.NewServer(t.Context(), "127.0.0.1:0", d, auth,
		rest.WithLogger(slog.New(slog.DiscardHandler)), rest.WithMCP(provider))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv.ListenerAddr().String()
}

func restRPC(t *testing.T, addr, method, params string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/v1/rpc", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer forged")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTransportPrincipals_NeverSocket (S4b review F3): REST and /mcp
// attribute a call only to its token or to env, so a token whose id
// spells a socket principal owns nothing of the socket's and cannot see
// its jobs.
func TestTransportPrincipals_NeverSocket(t *testing.T) {
	const forged = "socket:default"
	log := &callerLog{}
	scoped := serveREST(t, log, rest.NewScopedAuth(principalStore{forged}))
	restRPC(t, scoped, "status", `{}`)
	cs := connectMCP(t, scoped, "forged")
	if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "wa_status", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	restRPC(t, serveREST(t, log, rest.NewEnvTokenAuth("forged")), "status", `{}`)
	want := []app.Principal{app.TokenPrincipal(forged), app.TokenPrincipal(forged), app.EnvPrincipal}
	if len(log.seen) != len(want) {
		t.Fatalf("callers = %v, want %v", log.seen, want)
	}
	for i, p := range log.seen {
		if p != want[i] || p == app.SocketPrincipal("default") {
			t.Errorf("call %d attributed to %+v, want %+v", i, p, want[i])
		}
	}

	// End to end: the forged token is refused a socket job on both
	// transports (unknown to the token store, so before any lookup).
	release := make(chan struct{})
	defer close(release)
	slow := func(context.Context, json.RawMessage) (json.RawMessage, error) { <-release; return nil, nil }
	da, _ := jobsAdapter(t, nil, map[string]compositionHandler{"media.download": slow})
	job := mustRPC(t, socketCaller{da, "default"}, "media.download", `{"async":true}`)
	// The socket principal is the profile's: the same profile sees the job
	// on a later connection; another profile's socket needs the audited
	// override, which is refused while no audit log is wired.
	if got := mustRPC(t, socketCaller{da, "default"}, "jobs.get", jobArg(job["job_id"])); got["job_id"] != job["job_id"] {
		t.Errorf("same-profile socket jobs.get = %v, want the job", got)
	}
	if raw, err := (socketCaller{da, "work"}).Handle(t.Context(), "jobs.get", json.RawMessage(jobArg(job["job_id"]))); err == nil {
		t.Errorf("other-profile socket jobs.get = %s, want the unaudited override refused", raw)
	}
	// A call no transport attributed is a plain non-admin owner.
	if _, err := da.Handle(t.Context(), "jobs.get", json.RawMessage(jobArg(job["job_id"]))); !errors.Is(err, app.ErrJobNotFound) {
		t.Errorf("unattributed jobs.get of a socket job = %v, want job not found", err)
	}
	addr := serveREST(t, da, rest.NewScopedAuth(principalStore{forged}))
	if got := restRPC(t, addr, "jobs.get", jobArg(job["job_id"])); got["result"] != nil || got["error"] == nil {
		t.Errorf("REST jobs.get of a socket job = %v, want a refusal", got)
	}
	res, err := connectMCP(t, addr, "forged").CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_job_status", Arguments: map[string]any{"job_id": job["job_id"]},
	})
	if err != nil || !res.IsError || strings.Contains(toolText(res), `"state"`) {
		t.Errorf("/mcp wa_job_status of a socket job = %v %v, want a refusal", res, err)
	}
}

func toolText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestSafetyMethods_NotRefusedAtSaturation (ERRATA-2): with a principal's
// in-flight pool full, an ordinary call gets the typed capacity refusal
// on the wire while panic and session.logout still run.
func TestSafetyMethods_NotRefusedAtSaturation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		done := func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}
		slow := func(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
			<-release
			return done(ctx, p)
		}
		da, _ := jobsAdapter(t, nil, map[string]compositionHandler{
			"media.download": slow, "panic": done, "session.logout": done,
		})
		h := socketCaller{da, "default"}
		var wg sync.WaitGroup
		for range app.DefaultExecLimits.InFlightPer {
			wg.Go(func() { _, _ = h.Handle(t.Context(), "media.download", nil) })
		}
		synctest.Wait()

		_, err := h.Handle(t.Context(), "status", nil)
		if code, _, reason := app.RPCWire(err); code != -32001 || reason != domain.ReasonCapacityExceeded {
			t.Fatalf("ordinary call at saturation = %v (%d %s), want -32001 capacity_exceeded", err, code, reason)
		}
		for _, m := range []string{"panic", "session.logout"} {
			if got := mustRPC(t, h, m, `{}`); got["ok"] != true {
				t.Errorf("%s at saturation = %v, want it to run", m, got)
			}
		}
		close(release)
		wg.Wait()
	})
}
