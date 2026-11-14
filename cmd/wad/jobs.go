package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"sync/atomic"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// newExecutor builds da's dispatch boundary (FR-116-16). Token owners are
// revalidated in the REST token store; until REST opens it, none is valid.
func (da *dispatcherAdapter) newExecutor() *app.Executor {
	return app.NewExecutor(app.DefaultExecLimits, app.ExecAuth{
		Check: func(ctx context.Context, id string) (bool, error) {
			s := da.tokens.Load()
			if s == nil {
				return false, domain.ErrTokenInvalid
			}
			return s.Check(ctx, id)
		},
		Audit: overrideAudit{da},
	})
}

// overrideAudit denies admin overrides until the audit log is wired: an
// override that cannot be audited must not happen.
type overrideAudit struct{ da *dispatcherAdapter }

func (o overrideAudit) Record(ctx context.Context, e domain.AuditEvent) error {
	if o.da.audit == nil {
		return errors.New("audit log not wired")
	}
	return o.da.audit.Record(ctx, e)
}

// jobWire is a jobs.get/jobs.cancel answer: a failed job carries its
// error as the same (code, message, reason) row a synchronous call gets.
type jobWire struct {
	app.JobView
	Error map[string]any `json:"error,omitempty"`
}

type jobParams struct {
	JobID string `json:"job_id"`
}

// unattributed numbers the callers no transport attributed, so none can
// reach another's jobs (p8 S4c1 L2). Such a caller cannot poll its own
// job either: transports attribute theirs (WithCaller).
var unattributed atomic.Uint64

// Handle implements socket.Dispatcher and rest.Dispatcher: it runs method
// for ctx's caller under its deadline class, or answers jobs.get and
// jobs.cancel, read scope at REST and /mcp since only a job's owner (who
// held its method's scope; a token's never changes) or an audited admin
// reaches one. async:true rides in the params.
func (da *dispatcherAdapter) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	caller, ok := app.CallerFrom(ctx)
	if !ok { // no transport attributed it: a non-admin owner of its own, never socket or env
		caller = app.Principal{ID: "internal:" + strconv.FormatUint(unattributed.Add(1), 10)}
	}
	if method == "jobs.get" || method == "jobs.cancel" {
		var p jobParams
		if json.Unmarshal(params, &p) != nil || p.JobID == "" {
			return nil, app.ErrInvalidParams
		}
		op := da.exec.Get
		if method == "jobs.cancel" {
			op = da.exec.Cancel
		}
		v, err := op(ctx, caller, p.JobID)
		if err != nil {
			return nil, err
		}
		out := jobWire{JobView: v}
		if v.Err != nil {
			code, msg, reason := app.RPCWire(v.Err)
			out.Error = map[string]any{"code": code, "message": msg, "reason": reason}
		}
		return json.Marshal(out)
	}
	var a struct {
		Async bool `json:"async"`
	}
	_ = json.Unmarshal(params, &a) // absent or non-object params: not async; the handler validates them
	out, err := da.exec.Run(ctx, caller.ID, method, a.Async, func(wctx context.Context) (json.RawMessage, error) {
		return da.route(requestValues{wctx, ctx}, method, params)
	})
	if err == nil && method == "sync.force" && da.reach != nil {
		out = withReach(out, da.reach(ctx, params))
	}
	return out, err
}

// withReach adds sync.force's FR-116-25 verdict to a job answer, which the
// executor writes before the pull's own result exists. A completed call's
// answer already carries it and passes through.
func withReach(job json.RawMessage, verdict map[string]any) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(job, &m) != nil || m["job_id"] == nil || m["schema"] != nil {
		return job
	}
	maps.Copy(m, verdict)
	if b, err := json.Marshal(m); err == nil {
		return b
	}
	return job
}

// requestValues is the worker context (its deadline and cancellation)
// carrying the request's values: caller, trace span, request id.
type requestValues struct {
	context.Context
	req context.Context
}

func (c requestValues) Value(key any) any { return c.req.Value(key) }
