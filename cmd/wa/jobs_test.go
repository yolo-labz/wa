package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

func countCalls(fd *fakeDaemon, method string) (n int) {
	for _, c := range fd.seen() {
		if c.Method == method {
			n++
		}
	}
	return n
}

// TestCLI_PairPollsWithoutRestart: when pair answers with a job at the
// 10 s deadline, `wa pair` follows that job with jobs.get and never calls
// pair a second time, whether the job succeeds or fails.
func TestCLI_PairPollsWithoutRestart(t *testing.T) {
	old := jobPollEvery
	jobPollEvery = time.Millisecond
	t.Cleanup(func() { jobPollEvery = old })

	run := func(t *testing.T, final map[string]any) (stdout, stderr string, fd *fakeDaemon) {
		fd = newFakeDaemon(t)
		fd.on("pair", func(json.RawMessage) (any, *rpcError) {
			return map[string]any{"job_id": "j1", "state": "running", "timed_out": true}, nil
		})
		var mu sync.Mutex
		polls := 0
		fd.on("jobs.get", func(params json.RawMessage) (any, *rpcError) {
			var p struct {
				JobID string `json:"job_id"`
			}
			if _ = json.Unmarshal(params, &p); p.JobID != "j1" {
				return nil, &rpcError{Code: -32021, Message: "job not found"}
			}
			mu.Lock()
			defer mu.Unlock()
			if polls++; polls < 3 {
				return map[string]any{"job_id": "j1", "state": "running"}, nil
			}
			return final, nil
		})
		stdout, stderr = runCmd(t, "--socket", fd.path(), "--json", "pair")
		return stdout, stderr, fd
	}

	t.Run("done", func(t *testing.T) {
		stdout, stderr, fd := run(t, map[string]any{"job_id": "j1", "state": "done", "result": map[string]any{"paired": true}})
		if !strings.Contains(stdout, `"paired":true`) {
			t.Fatalf("stdout = %q, stderr = %q; want the job's pair result", stdout, stderr)
		}
		if p, g := countCalls(fd, "pair"), countCalls(fd, "jobs.get"); p != 1 || g != 3 {
			t.Fatalf("pair called %d times, jobs.get %d; want 1 and 3", p, g)
		}
	})
	t.Run("failed", func(t *testing.T) {
		stdout, stderr, fd := run(t, map[string]any{
			"job_id": "j1", "state": "failed",
			"error": map[string]any{"code": -32019, "message": "session wiped; restart the daemon before pairing"},
		})
		if !strings.Contains(stderr, "session wiped") || strings.Contains(stdout, "paired") {
			t.Fatalf("stdout = %q, stderr = %q; want the job's error", stdout, stderr)
		}
		if p := countCalls(fd, "pair"); p != 1 {
			t.Fatalf("pair called %d times after a failed job; want 1", p)
		}
	})
}

// TestCLI_WaitHonoursOverallDeadline: `wa wait` re-polls the daemon's
// capped wait in bounded slices and stops at the caller's own --timeout;
// an event on a later poll is returned, and a -32003 that is not the
// daemon's wait timeout is not re-polled.
func TestCLI_WaitHonoursOverallDeadline(t *testing.T) {
	old := waitPollMax
	waitPollMax = 40 * time.Millisecond
	t.Cleanup(func() { waitPollMax = old })
	timedOut := &rpcError{Code: rpcWaitTimeout, Message: "wait timeout", Data: json.RawMessage(`{"reason":"timed_out"}`)}

	t.Run("budget", func(t *testing.T) {
		fd := newFakeDaemon(t)
		var mu sync.Mutex
		var slices []int64
		fd.on("wait", func(params json.RawMessage) (any, *rpcError) {
			var p struct {
				TimeoutMs int64 `json:"timeoutMs"`
			}
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			slices = append(slices, p.TimeoutMs)
			mu.Unlock()
			<-time.After(time.Duration(p.TimeoutMs) * time.Millisecond) // the daemon blocks for the slice
			return nil, timedOut
		})
		start := time.Now()
		_, stderr := runCmd(t, "--socket", fd.path(), "wait", "--timeout", "300ms")
		el := time.Since(start)
		if !strings.Contains(stderr, "wait timeout") {
			t.Fatalf("stderr = %q, want the wait timeout", stderr)
		}
		mu.Lock()
		defer mu.Unlock()
		var sum int64
		for _, s := range slices {
			if s <= 0 || s > 40 {
				t.Errorf("poll asked for %d ms, want 1..40", s)
			}
			sum += s
		}
		if len(slices) < 2 || sum > 301 || el < 290*time.Millisecond || el > 3*time.Second {
			t.Fatalf("polls %v (sum %d ms) over %v; want repeated slices filling a 300 ms budget and no more", slices, sum, el)
		}
	})
	t.Run("event on a later poll", func(t *testing.T) {
		fd := newFakeDaemon(t)
		var mu sync.Mutex
		n := 0
		fd.on("wait", func(json.RawMessage) (any, *rpcError) {
			mu.Lock()
			defer mu.Unlock()
			if n++; n == 1 {
				return nil, timedOut
			}
			return map[string]any{"type": "message"}, nil
		})
		stdout, stderr := runCmd(t, "--socket", fd.path(), "wait", "--timeout", "5s")
		if !strings.Contains(stdout, `"type":"message"`) || countCalls(fd, "wait") != 2 {
			t.Fatalf("stdout = %q, stderr = %q, %d calls; want the event from the 2nd poll", stdout, stderr, countCalls(fd, "wait"))
		}
	})
	t.Run("other -32003 is final", func(t *testing.T) {
		fd := newFakeDaemon(t)
		fd.on("wait", func(json.RawMessage) (any, *rpcError) {
			return nil, &rpcError{Code: rpcWaitTimeout, Message: "RequestTimeoutDuringShutdown", Data: json.RawMessage(`{"reason":"disconnected"}`)}
		})
		_, stderr := runCmd(t, "--socket", fd.path(), "wait", "--timeout", "5s")
		if !strings.Contains(stderr, "RequestTimeoutDuringShutdown") || countCalls(fd, "wait") != 1 {
			t.Fatalf("stderr = %q, %d calls; want one call and its error", stderr, countCalls(fd, "wait"))
		}
	})
}

// TestCLI_PairInterruptCancelsJob (p8 S4c1 L1): once pair is a job, the
// daemon keeps pairing (and rotating the QR) until it ends, so an interrupt
// of `wa pair` must send jobs.cancel for that job before the CLI exits, and
// must not wait out the poll interval or budget.
func TestCLI_PairInterruptCancelsJob(t *testing.T) {
	fd := newFakeDaemon(t)
	polled := make(chan struct{}, 1)
	fd.on("jobs.get", func(json.RawMessage) (any, *rpcError) {
		select {
		case polled <- struct{}{}:
		default:
		}
		return map[string]any{"job_id": "j1", "state": "running"}, nil
	})
	fd.on("jobs.cancel", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"job_id": "j1", "state": "cancellation_requested"}, nil
	})

	ctx, interrupt := context.WithCancel(t.Context())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, _, err := awaitJob(ctx, fd.path(), json.RawMessage(`{"job_id":"j1","state":"running","timed_out":true}`), time.Minute)
		done <- err
	}()
	<-polled
	interrupt()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an interrupted wait returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("awaitJob ignored the interrupt")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("interrupt took %v", time.Since(start))
	}
	var got struct {
		JobID string `json:"job_id"`
	}
	for _, c := range fd.seen() {
		if c.Method == "jobs.cancel" {
			_ = json.Unmarshal(c.Params, &got)
		}
	}
	if got.JobID != "j1" {
		t.Fatalf("jobs.cancel job_id = %q after the interrupt; want j1 (calls: %d jobs.get, %d jobs.cancel)",
			got.JobID, countCalls(fd, "jobs.get"), countCalls(fd, "jobs.cancel"))
	}
}

// TestWaitHelp_DocumentsDaemonCap (p8 S4c1 I2): `wa wait --help` says that
// the daemon answers one wait within 9.5 s and that the CLI re-polls up to
// --timeout, so a script author is not surprised by the lower socket default.
func TestWaitHelp_DocumentsDaemonCap(t *testing.T) {
	long := waitCmd.Long
	for _, want := range []string{"9.5", "re-poll", "--timeout"} {
		if !strings.Contains(long, want) {
			t.Errorf("wa wait Long help lacks %q:\n%s", want, long)
		}
	}
}

// TestCLI_SyncForceFollowsJob: a chat pull promoted to a job at 10 s is
// followed with jobs.get, never re-sent, and prints the job's own result,
// gap verdict included.
func TestCLI_SyncForceFollowsJob(t *testing.T) {
	old := jobPollEvery
	jobPollEvery = time.Millisecond
	t.Cleanup(func() { jobPollEvery = old })
	fd := newFakeDaemon(t)
	fd.on("sync.force", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"job_id": "s1", "state": "running", "timed_out": true, "gap_reachable": false, "reason": "tail_gap_unanchored"}, nil
	})
	fd.on("jobs.get", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"job_id": "s1", "state": "done", "result": map[string]any{
			"schema": "wa.sync.force/v1", "delivered": true, "received": 12, "gap_reachable": false, "reason": "tail_gap_unanchored",
		}}, nil
	})
	stdout, stderr := runCmd(t, "--socket", fd.path(), "--json", "sync", "force", "--chat", "15550000001@s.whatsapp.net")
	if !strings.Contains(stdout, `"received":12`) || !strings.Contains(stdout, "tail_gap_unanchored") {
		t.Fatalf("stdout = %q, stderr = %q; want the job's sync result", stdout, stderr)
	}
	if p, g := countCalls(fd, "sync.force"), countCalls(fd, "jobs.get"); p != 1 || g != 1 {
		t.Fatalf("sync.force called %d times, jobs.get %d; want 1 and 1", p, g)
	}
}

// TestCLI_InterruptInFirstWindowEndsCall (WA-S4C2-FIX L1): the first call of
// `wa sync force` / `wa pair` has no job id, so Ctrl-C inside the 10 s window
// had nothing to cancel and, with the handler installed only after that call,
// killed the process mid-write. The handler is now installed first and the
// interrupt closes the call's socket, which is how the daemon cancels it.
func TestCLI_InterruptInFirstWindowEndsCall(t *testing.T) {
	for _, args := range [][]string{
		{"sync", "force", "--chat", "15550000001@s.whatsapp.net"},
		{"pair"},
	} {
		method := map[string]string{"sync": "sync.force", "pair": "pair"}[args[0]]
		t.Run(args[0], func(t *testing.T) {
			fd := newFakeDaemon(t)
			release := make(chan struct{})
			started := make(chan struct{}, 1)
			t.Cleanup(func() { close(release) })
			fd.on(method, func(json.RawMessage) (any, *rpcError) {
				started <- struct{}{}
				<-release
				return map[string]any{}, nil
			})
			go func() {
				<-started
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT) // the client is killed 1 s into the window
			}()
			done := make(chan string, 1)
			go func() {
				_, stderr := runCmd(t, append([]string{"--socket", fd.path()}, args...)...)
				done <- stderr
			}()
			select {
			case stderr := <-done:
				if !strings.Contains(stderr, "interrupted") {
					t.Errorf("stderr = %q, want the interrupt reported", stderr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the first call ignored the interrupt")
			}
		})
	}
}

// TestCLI_RemoteInterruptInFirstCall (WA-S4C2-FIX-FOLLOW, p8 L1): with
// --remote the first call's interrupt used to be swallowed (callRemote took
// no ctx): the CLI waited out the slow answer and exited 0. It must now
// abandon the request at once with exit 130.
func TestCLI_RemoteInterruptInFirstCall(t *testing.T) {
	t.Setenv("WA_REMOTE_INSECURE", "1")
	t.Setenv("WA_TOKEN", "x")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer srv.Close()
	old := flagRemote
	flagRemote = srv.URL
	t.Cleanup(func() { flagRemote = old })

	ctx, interrupt := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, interrupt)
	start := time.Now()
	_, code, err := callJob(ctx, "unused.sock", "sync.force", map[string]any{"count": 1}, time.Second)
	if el := time.Since(start); code != 130 || err == nil || el > time.Second {
		t.Fatalf("--remote interrupted at 100ms: returned after %v with code=%d err=%v, want 130 and an error at once", el, code, err)
	}
}

// TestCLI_SendFollowsJobNeverResends (WA-S4C1-SEND-DEADLINE): a send past the
// daemon's 45 s return deadline answers {job_id, timed_out}; `wa send` polls
// jobs.get and prints the send's own result. Calling send again would
// duplicate the message to a real person.
func TestCLI_SendFollowsJobNeverResends(t *testing.T) {
	old := jobPollEvery
	jobPollEvery = time.Millisecond
	t.Cleanup(func() { jobPollEvery = old })
	fd := newFakeDaemon(t)
	fd.on("send", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"job_id": "m1", "state": "running", "timed_out": true}, nil
	})
	fd.on("jobs.get", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"job_id": "m1", "state": "done", "result": map[string]any{"messageId": "3EB0SENT", "status": "sent"}}, nil
	})
	stdout, stderr := runCmd(t, "--socket", fd.path(), "--json", "send", "--to", "15550000001@s.whatsapp.net", "--body", "hi")
	if !strings.Contains(stdout, "3EB0SENT") || strings.Contains(stdout, "job_id") {
		t.Fatalf("stdout = %q, stderr = %q; want the send's own result, not the envelope", stdout, stderr)
	}
	if s, g := countCalls(fd, "send"), countCalls(fd, "jobs.get"); s != 1 || g != 1 {
		t.Fatalf("send called %d times, jobs.get %d; want 1 and 1", s, g)
	}
}

// TestCLI_EverySendClassMethodFollowsItsJob (p8 L1): derived from the class
// table, not from a command list. Whatever the method, an envelope from the
// first call is followed with jobs.get and never answered with a second call
// of that method; a non-send method keeps the one-shot path.
func TestCLI_EverySendClassMethodFollowsItsJob(t *testing.T) {
	old := jobPollEvery
	jobPollEvery = time.Millisecond
	t.Cleanup(func() { jobPollEvery = old })
	for m, d := range app.MethodDeadlines {
		if m == "jobs.get" || m == "jobs.cancel" {
			continue // the poll itself
		}
		if (d.Return == app.SendReturnDeadline) != app.IsSendClass(m) {
			t.Fatalf("%s: IsSendClass disagrees with the class table", m)
		}
		fd := newFakeDaemon(t)
		fd.on(m, func(json.RawMessage) (any, *rpcError) {
			return map[string]any{"job_id": "j1", "state": "running", "timed_out": true}, nil
		})
		fd.on("jobs.get", func(json.RawMessage) (any, *rpcError) {
			return map[string]any{"job_id": "j1", "state": "done", "result": map[string]any{"ok": m}}, nil
		})
		got, _, err := callAndClose(fd.path(), m, map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if app.IsSendClass(m) {
			if !strings.Contains(string(got), `"ok"`) || countCalls(fd, m) != 1 || countCalls(fd, "jobs.get") != 1 {
				t.Errorf("%s: %s (calls %d, jobs.get %d), want the job's result after one call", m, got, countCalls(fd, m), countCalls(fd, "jobs.get"))
			}
		} else if !strings.Contains(string(got), "job_id") || countCalls(fd, "jobs.get") != 0 {
			t.Errorf("%s (not send class): %s, want the bare envelope", m, got)
		}
	}
}

// TestSendTimeouts_BracketTheReturnDeadline (p8 L2): the remote ceiling and
// the job budget are not free constants. A slow send must outlive neither.
func TestSendTimeouts_BracketTheReturnDeadline(t *testing.T) {
	worker := app.MethodDeadlines["send"].MaxWorker
	if remoteCallTimeout < 2*app.SendReturnDeadline || remoteCallTimeout > worker {
		t.Errorf("remoteCallTimeout %v: want between 2x the send return deadline (%v) and a promoted worker's life (%v)", remoteCallTimeout, 2*app.SendReturnDeadline, worker)
	}
	if sendJobBudget <= worker || sendJobBudget > worker+30*time.Second {
		t.Errorf("sendJobBudget %v: want just over a promoted worker's life %v", sendJobBudget, worker)
	}
}
