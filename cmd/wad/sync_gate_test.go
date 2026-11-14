package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	gateOK    = "15550000001@s.whatsapp.net"
	gateOther = "15550000002@s.whatsapp.net"
)

// fakeReach records the chats it was asked a verdict for.
type fakeReach struct{ asked []domain.JID }

func (f *fakeReach) Reachability(_ context.Context, chat domain.JID, _ bool) wmAdapter.ForceSyncResult {
	f.asked = append(f.asked, chat)
	no := true
	if chat.IsZero() {
		return wmAdapter.ForceSyncResult{Chats: []wmAdapter.ChatSync{{Chat: "sweep"}}}
	}
	return wmAdapter.ForceSyncResult{GapReachable: &no, Reason: wmAdapter.ReasonPostRecoveryAnchor}
}

func gateAllow(actions ...domain.Action) *domain.Allowlist {
	a := domain.NewAllowlist()
	a.Grant(domain.MustJID(gateOK), actions...)
	return a
}

// TestSyncForce_ChatScopedPullNeedsReadAllowlist (WA-S4C2-FIX M1): a pull
// lands older history in the readable store, so a chat the allowlist does
// not grant ActionRead is refused with the typed not_allowlisted refusal
// before any pull. A send-only grant is not a read grant.
func TestSyncForce_ChatScopedPullNeedsReadAllowlist(t *testing.T) {
	for name, tc := range map[string]struct {
		allow *domain.Allowlist
		chat  string
		pulls int32
	}{
		"not allowlisted":  {gateAllow(domain.ActionRead), gateOther, 0},
		"send only":        {gateAllow(domain.ActionSend), gateOK, 0},
		"read granted":     {gateAllow(domain.ActionRead), gateOK, 1},
		"empty allowlist":  {domain.NewAllowlist(), gateOK, 0},
		"granted, no chat": {gateAllow(domain.ActionRead), "", 1}, // the sweep filters inside the adapter
	} {
		t.Run(name, func(t *testing.T) {
			f := &slowForcer{release: make(chan struct{})}
			params := `{"count":1}`
			if tc.chat != "" {
				params = `{"count":1,"chat":"` + tc.chat + `"}`
			}
			_, err := makeSyncForceHandler(f, tc.allow)(t.Context(), json.RawMessage(params))
			if tc.pulls == 0 && !errors.Is(err, app.ErrNotAllowlisted) {
				t.Errorf("err = %v, want ErrNotAllowlisted", err)
			}
			if tc.pulls == 1 && err != nil {
				t.Errorf("err = %v, want the pull", err)
			}
			if n := f.calls.Load(); n != tc.pulls {
				t.Errorf("pulls = %d, want %d", n, tc.pulls)
			}
		})
	}
}

// filterForcer reports the sweep filter its ctx carries.
type filterForcer struct{ filter func(domain.JID) bool }

func (f *filterForcer) ForceSync(ctx context.Context, _ domain.JID, _ int, _ bool) (wmAdapter.ForceSyncResult, error) {
	f.filter = wmAdapter.ChatFilter(ctx)
	return wmAdapter.ForceSyncResult{}, nil
}

// TestSyncForce_SweepCarriesReadFilter (M1): the global sweep reaches the
// adapter with a filter that keeps only chats granted ActionRead.
func TestSyncForce_SweepCarriesReadFilter(t *testing.T) {
	f := &filterForcer{}
	if _, err := makeSyncForceHandler(f, gateAllow(domain.ActionRead))(t.Context(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if f.filter == nil || !f.filter(domain.MustJID(gateOK)) || f.filter(domain.MustJID(gateOther)) {
		t.Fatal("the sweep must carry a filter keeping only the read-allowlisted chats")
	}
}

// TestSyncReach_NoVerdictForRefusedOrUnparsableChat (M1, L2): the job
// answer carries no verdict, and the adapter is not asked, for a chat the
// call will refuse; an unparsable chat no longer turns into the sweep's
// per-chat verdicts.
func TestSyncReach_NoVerdictForRefusedOrUnparsableChat(t *testing.T) {
	for name, params := range map[string]string{
		"unparsable":      `{"chat":"abc@s.whatsapp.net"}`,
		"not allowlisted": `{"chat":"` + gateOther + `"}`,
	} {
		f := &fakeReach{}
		if got := syncReach(f, gateAllow(domain.ActionRead))(t.Context(), json.RawMessage(params)); len(got) != 0 || len(f.asked) != 0 {
			t.Errorf("%s: verdict %v, asked %v; want none", name, got, f.asked)
		}
	}
	f := &fakeReach{}
	reach := syncReach(f, gateAllow(domain.ActionRead))
	if got := reach(t.Context(), json.RawMessage(`{"chat":"`+gateOK+`"}`)); got["gap_reachable"] != true {
		t.Errorf("allowlisted chat verdict = %v", got)
	}
	if got := reach(t.Context(), json.RawMessage(`{}`)); got["chats"] == nil {
		t.Errorf("sweep verdict = %v, want chats[]", got)
	}
}

// TestSyncForce_AsyncRefusalThroughJob (M1): an async pull of a chat that
// is not allowlisted answers with no verdict, and its job fails with the
// not_allowlisted reason rather than pulling.
func TestSyncForce_AsyncRefusalThroughJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &slowForcer{release: make(chan struct{})}
		h := syncJobsAdapter(t, f)
		h.reach = syncReach(&fakeReach{}, jobsAllow)
		job := mustRPC(t, h, "sync.force", `{"async":true,"count":1,"chat":"`+gateOther+`"}`)
		if job["job_id"] == nil || job["gap_reachable"] != nil || job["chats"] != nil {
			t.Fatalf("job answer = %v, want a job with no verdict", job)
		}
		synctest.Wait()
		got := mustRPC(t, h, "jobs.get", jobArg(job["job_id"]))
		errObj, _ := got["error"].(map[string]any)
		if got["state"] == app.StateDone || errObj["reason"] != string(domain.ReasonNotAllowlisted) || f.calls.Load() != 0 {
			t.Fatalf("job = %v, pulls %d; want a not_allowlisted failure and no pull", got, f.calls.Load())
		}
	})
}
