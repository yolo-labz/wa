package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// slowForcer is a ForceSync that blocks until release (ignoring ctx, like
// a stuck pull) and reports the gap verdict a tail gap gets.
type slowForcer struct {
	release chan struct{}
	calls   atomic.Int32
}

func (f *slowForcer) ForceSync(_ context.Context, chat domain.JID, count int, _ bool) (wmAdapter.ForceSyncResult, error) {
	f.calls.Add(1)
	if count == 7 { // the "slow" request
		<-f.release
	}
	no := false
	return wmAdapter.ForceSyncResult{
		Requested: false, Connected: true, Chat: chat.String(), Count: count,
		GapReachable: &no, Reason: wmAdapter.ReasonTailGapUnanchored,
	}, nil
}

func syncJobsAdapter(t *testing.T, f *slowForcer) socketCaller {
	t.Helper()
	da, _ := jobsAdapter(t, nil, map[string]compositionHandler{"sync.force": makeSyncForceHandler(f)})
	return socketCaller{da, "default"}
}

// TestSyncForce_SyncModeDeadline: without async, a fast sync.force keeps its
// synchronous shape and a slow one answers {job_id, running, timed_out} at
// exactly 10 s, then reports its one result through the job; async:true
// queues at once. The pull never runs twice.
func TestSyncForce_SyncModeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &slowForcer{release: make(chan struct{})}
		h := syncJobsAdapter(t, f)

		if got := mustRPC(t, h, "sync.force", `{"count":1}`); got["schema"] != "wa.sync.force/v1" || got["job_id"] != nil {
			t.Fatalf("fast sync.force = %v, want the synchronous result", got)
		}
		start := time.Now()
		job := mustRPC(t, h, "sync.force", `{"count":7}`)
		if el := time.Since(start); el != app.ReturnDeadline || job["state"] != app.StateRunning || job["timed_out"] != true {
			t.Fatalf("slow sync.force answered %v after %v, want running+timed_out at %v", job, el, app.ReturnDeadline)
		}
		close(f.release)
		synctest.Wait()
		got := mustRPC(t, h, "jobs.get", jobArg(job["job_id"]))
		if res, _ := got["result"].(map[string]any); got["state"] != app.StateDone || res["schema"] != "wa.sync.force/v1" {
			t.Fatalf("finished job = %v", got)
		}
		start = time.Now()
		if q := mustRPC(t, h, "sync.force", `{"async":true,"count":1}`); time.Since(start) != 0 || q["state"] != app.StateQueued {
			t.Fatalf("async = %v, want queued at once", q)
		}
		synctest.Wait()
		if n := f.calls.Load(); n != 3 {
			t.Fatalf("sync.force ran %d times, want 3 (never re-run)", n)
		}
	})
}

// TestSyncForce_JobCarriesGapReachability (FR-116-25): the initial job
// answer, whether promoted at 10 s or queued by async:true, carries the same
// gap_reachable and machine reason as the finished result, per chat for a
// sweep; a lookup that cannot decide says false/gap_state_unknown.
func TestSyncForce_JobCarriesGapReachability(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &slowForcer{release: make(chan struct{})}
		h := syncJobsAdapter(t, f)
		h.dispatcherAdapter.reach = func(_ context.Context, raw json.RawMessage) map[string]any {
			var p struct{ Chat string }
			_ = json.Unmarshal(raw, &p)
			if p.Chat == "" {
				return map[string]any{"chats": []wmAdapter.ChatSync{{Chat: "a", Reason: "no_pending_gap"}}}
			}
			return map[string]any{"gap_reachable": false, "reason": wmAdapter.ReasonTailGapUnanchored}
		}
		const chat = "15550000001@s.whatsapp.net"
		want := func(t *testing.T, where string, got map[string]any) {
			t.Helper()
			if v, ok := got["gap_reachable"].(bool); !ok || v || got["reason"] != wmAdapter.ReasonTailGapUnanchored {
				t.Errorf("%s = %v, want gap_reachable:false reason:tail_gap_unanchored", where, got)
			}
		}

		queued := mustRPC(t, h, "sync.force", `{"async":true,"chat":"`+chat+`","count":7}`)
		if queued["state"] != app.StateQueued {
			t.Fatalf("async = %v", queued)
		}
		want(t, "queued job answer", queued)
		promoted := mustRPC(t, h, "sync.force", `{"chat":"`+chat+`","count":7}`)
		if promoted["timed_out"] != true {
			t.Fatalf("slow call = %v, want the 10 s job answer", promoted)
		}
		want(t, "promoted job answer", promoted)
		other := socketCaller{h.dispatcherAdapter, "other"} // its own owner, so the 2-job quota is free
		sweep := mustRPC(t, other, "sync.force", `{"async":true,"count":1}`)
		if cs, _ := sweep["chats"].([]any); len(cs) != 1 || sweep["gap_reachable"] != nil {
			t.Errorf("sweep job answer = %v, want per-chat verdicts and no single optimistic one", sweep)
		}

		close(f.release)
		synctest.Wait()
		for _, j := range []map[string]any{queued, promoted} {
			got := mustRPC(t, h, "jobs.get", jobArg(j["job_id"]))
			res, _ := got["result"].(map[string]any)
			want(t, "final result", res)
		}
		if got := mustRPC(t, h, "sync.force", `{"chat":"`+chat+`","count":1}`); got["job_id"] != nil {
			t.Errorf("a completed call must keep its synchronous shape, got %v", got)
		}
	})
}
