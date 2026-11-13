package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
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
