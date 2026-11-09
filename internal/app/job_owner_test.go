package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeTokens is the token store as OwnerCheck sees it: id → scope; an
// absent id is revoked, expired or missing. It records every check.
type fakeTokens struct {
	mu     sync.Mutex
	scope  map[string]string
	checks []string
}

func (f *fakeTokens) check(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, id)
	s, ok := f.scope[id]
	if !ok {
		return false, errors.New("token revoked")
	}
	return s == "admin", nil
}

func (f *fakeTokens) set(id, scope string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if scope == "" {
		delete(f.scope, id)
	} else {
		f.scope[id] = scope
	}
}

func (f *fakeTokens) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks)
}

type jobAudit struct {
	mu     sync.Mutex
	events []domain.AuditEvent
	fail   error
}

func (a *jobAudit) Record(_ context.Context, e domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return a.fail
	}
	a.events = append(a.events, e)
	return nil
}

func (a *jobAudit) take() []domain.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.events
	a.events = nil
	return out
}

func ownerTest(t *testing.T, scopes map[string]string, body func(t *testing.T, e *Executor, tokens *fakeTokens, audit *jobAudit)) {
	t.Helper()
	tokens, audit := &fakeTokens{scope: scopes}, &jobAudit{}
	limits := DefaultExecLimits
	limits.JobsPer = 4
	executorTestWith(t, limits, ExecAuth{Check: tokens.check, Audit: audit}, func(t *testing.T, e *Executor) {
		body(t, e, tokens, audit)
	})
}

func stateOf(e *Executor, id string) (string, []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.execs[id].state, e.execs[id].result
}

func wantOverride(t *testing.T, audit *jobAudit, actor, job, owner string) {
	t.Helper()
	ev := audit.take()
	if len(ev) != 1 || ev[0].Action != domain.AuditJobAdminAccess || ev[0].Actor != actor ||
		ev[0].Detail != "job="+job+" owner="+owner || ev[0].Action.String() != "job_admin_access" {
		t.Fatalf("override audit %+v, want one job_admin_access by %s", ev, actor)
	}
}

// TestSyncJobs_CrossTokenDenied: a non-owner below admin — another
// token, a read token, a token claiming Admin in-process — gets
// job_not_found for get and cancel, identical to an unknown id, and the
// job is untouched; a revoked caller is refused outright.
func TestSyncJobs_CrossTokenDenied(t *testing.T) {
	ownerTest(t, map[string]string{"a": "send", "b": "send", "r": "read"}, func(t *testing.T, e *Executor, tokens *fakeTokens, audit *jobAudit) {
		f := newFake(0)
		j := startJob(t, e, TokenPrincipal("a").ID, f)
		_, unknown := e.Get(t.Context(), TokenPrincipal("b"), "00000000000000000000000000000000")
		forged := TokenPrincipal("b")
		forged.Admin = true // a token's admin comes from the store only
		for _, p := range []Principal{TokenPrincipal("b"), TokenPrincipal("r"), forged, {ID: "socket-lookalike"}} {
			for op, call := range map[string]func(context.Context, Principal, string) (JobView, error){"get": e.Get, "cancel": e.Cancel} {
				if v, err := call(t.Context(), p, j.JobID); !errors.Is(err, ErrJobNotFound) || err.Error() != unknown.Error() || v.ID != "" || v.State != "" {
					t.Errorf("%s by %+v: %+v %v, want the unknown-id answer", op, p, v, err)
				}
			}
		}
		if st, _ := stateOf(e, j.JobID); st != StateRunning || f.calls.Load() != 1 || len(audit.take()) != 0 {
			t.Fatalf("job disturbed: %s", st)
		}
		f.free()
		synctest.Wait()
		if _, err := e.Get(t.Context(), TokenPrincipal("b"), j.JobID); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("non-owner read a finished result: %v", err)
		}
		wantState(t, e, TokenPrincipal("a").ID, j.JobID, StateDone)
		tokens.set("b", "")
		if _, err := e.Get(t.Context(), TokenPrincipal("b"), j.JobID); !errors.Is(err, ErrPrincipalInvalid) {
			t.Errorf("revoked caller: %v", err)
		}
	})
}

// TestSyncJobs_AdminOverrideAudited: an admin token reads and cancels
// another's job only through the override, one job_admin_access record
// per access; owners and own jobs are not audited; a failed audit or a
// store-side demotion denies.
func TestSyncJobs_AdminOverrideAudited(t *testing.T) {
	ownerTest(t, map[string]string{"a": "send", "adm": "admin"}, func(t *testing.T, e *Executor, tokens *fakeTokens, audit *jobAudit) {
		a, adm := TokenPrincipal("a"), TokenPrincipal("adm")
		j1, j2 := startJob(t, e, a.ID, newFake(0)), startJob(t, e, a.ID, newFake(0))
		if v, err := e.Get(t.Context(), adm, j1.JobID); err != nil || v.State != StateRunning {
			t.Fatalf("override get: %+v %v", v, err)
		}
		wantOverride(t, audit, adm.ID, j1.JobID, a.ID)
		if v, err := e.Cancel(t.Context(), adm, j1.JobID); err != nil || v.State != StateCancelRequested {
			t.Fatalf("override cancel: %+v %v", v, err)
		}
		wantOverride(t, audit, adm.ID, j1.JobID, a.ID)
		own := startJob(t, e, adm.ID, newFake(0))
		_, err1 := e.Get(t.Context(), a, j1.JobID)
		_, err2 := e.Get(t.Context(), adm, own.JobID)
		if err1 != nil || err2 != nil || len(audit.take()) != 0 {
			t.Fatalf("owner access audited or refused: %v %v", err1, err2)
		}
		audit.fail = errors.New("disk full")
		if _, err := e.Cancel(t.Context(), adm, j2.JobID); err == nil {
			t.Error("override served without its audit record")
		}
		if st, _ := stateOf(e, j2.JobID); st != StateRunning {
			t.Errorf("unaudited cancel took effect: %s", st)
		}
		audit.fail = nil
		tokens.set("adm", "send")
		if _, err := e.Get(t.Context(), adm, j2.JobID); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("demoted admin kept the override: %v", err)
		}
	})
}

// TestSyncJobs_CancelledOnTokenRevoke: within one 30 s sweep of an
// external revocation, with no one polling, the owner's running work is
// cancelled (a resistant worker keeps its slot until it exits and its
// late result is dropped) and its unfetched result is gone.
func TestSyncJobs_CancelledOnTokenRevoke(t *testing.T) {
	ownerTest(t, map[string]string{"a": "send", "adm": "admin", "k": "send"}, func(t *testing.T, e *Executor, tokens *fakeTokens, _ *jobAudit) {
		a := TokenPrincipal("a").ID
		stubborn, coop, quick := newFake(0), newFake(0), newFake(time.Second)
		coop.honourCtx = true
		js, jc, jq := startJob(t, e, a, stubborn).JobID, startJob(t, e, a, coop).JobID, startJob(t, e, a, quick).JobID
		kept := startJob(t, e, TokenPrincipal("k").ID, newFake(0))
		<-time.After(2 * time.Second)
		tokens.set("a", "")
		<-time.After(ownerSweepEvery)
		for id, want := range map[string]string{js: StateCancelRequested, jc: StateCancelled, jq: StateExpired, kept.JobID: StateRunning} {
			if st, res := stateOf(e, id); st != want || res != nil {
				t.Errorf("%s after sweep: %s %s, want %s", id, st, res, want)
			}
		}
		wantCounts(t, e, 0, 2) // the resistant worker and k's job
		stubborn.free()
		synctest.Wait()
		if st, res := stateOf(e, js); st != StateCancelled || res != nil {
			t.Errorf("late result of a revoked owner: %s %s", st, res)
		}
		wantCounts(t, e, 0, 1)
		if v, err := e.Get(t.Context(), TokenPrincipal("adm"), jq); err != nil || v.Result != nil {
			t.Errorf("override returned a revoked owner's result: %+v %v", v, err)
		}
	})
}

// TestSyncJobs_SocketPrincipal: the socket principal is admin, the same
// across connections, owns socket-started work without store checks or
// audit, and reaches token-owned work only through the audited
// override; tokens below admin cannot reach its work.
func TestSyncJobs_SocketPrincipal(t *testing.T) {
	ownerTest(t, map[string]string{"a": "send", "r": "read"}, func(t *testing.T, e *Executor, tokens *fakeTokens, audit *jobAudit) {
		conn1, conn2 := SocketPrincipal("default"), SocketPrincipal("default")
		if conn1 != conn2 || !conn1.Admin {
			t.Fatalf("socket principal %+v / %+v", conn1, conn2)
		}
		js := startJob(t, e, conn1.ID, newFake(0))
		if v, err := e.Cancel(t.Context(), conn2, js.JobID); err != nil || v.State != StateCancelRequested {
			t.Fatalf("socket cancel of its own job from a new connection: %+v %v", v, err)
		}
		if len(audit.take()) != 0 || slices.ContainsFunc(tokens.seen(), func(c string) bool { return strings.HasPrefix(c, "socket") }) {
			t.Error("socket-owned access was audited or store-checked")
		}
		for _, p := range []Principal{TokenPrincipal("a"), TokenPrincipal("r")} {
			if _, err := e.Get(t.Context(), p, js.JobID); !errors.Is(err, ErrJobNotFound) {
				t.Errorf("%s reached socket work: %v", p.ID, err)
			}
		}
		ja := startJob(t, e, TokenPrincipal("a").ID, newFake(0))
		if _, err := e.Get(t.Context(), conn1, ja.JobID); err != nil {
			t.Fatalf("socket override: %v", err)
		}
		wantOverride(t, audit, "socket:default", ja.JobID, TokenPrincipal("a").ID)
		if v, err := e.Get(t.Context(), EnvPrincipal, ja.JobID); err != nil || v.State != StateRunning {
			t.Fatalf("env (implicit admin) override: %+v %v", v, err)
		}
		wantOverride(t, audit, "env", ja.JobID, TokenPrincipal("a").ID)
	})
}

// startJob admits async work and lets its worker reach running.
func startJob(t *testing.T, e *Executor, owner string, f *fake) reply {
	t.Helper()
	r := runAsync(t, e, owner, f)
	synctest.Wait()
	return r
}
