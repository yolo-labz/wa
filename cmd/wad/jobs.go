package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// newExecutor builds the dispatch boundary for da (FR-116-16). Token
// owners are revalidated against the REST token store, once the REST
// listener opened it; with no store there is no valid token.
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

// jobParams is what jobs.get and jobs.cancel decode.
type jobParams struct {
	JobID string `json:"job_id"`
}

// dispatch runs method for ctx's caller under its deadline class, or
// answers jobs.get / jobs.cancel. Both are read scope at REST and /mcp:
// only a job's owner (who held its method's scope to start it; a token's
// scope never changes) or an audited admin reaches it, so cancel always
// has the initiating method's scope. async:true rides in the params.
func (da *dispatcherAdapter) dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	caller, ok := app.CallerFrom(ctx)
	if !ok { // no transport attributed it: a non-admin owner, never socket or env
		caller = app.Principal{ID: "internal"}
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
	return da.exec.Run(ctx, caller.ID, method, a.Async, func(wctx context.Context) (json.RawMessage, error) {
		return da.route(requestValues{wctx, ctx}, method, params)
	})
}

// requestValues is the worker context (its deadline and cancellation)
// carrying the request's values: caller, trace span, request id.
type requestValues struct {
	context.Context
	req context.Context
}

func (c requestValues) Value(key any) any { return c.req.Value(key) }
