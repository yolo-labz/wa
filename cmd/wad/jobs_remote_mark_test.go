package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestJobWorker_CarriesTheTransportMark pins the S4c1 / MCP-SEC seam: the
// executor's worker context is built from the daemon's lifecycle context,
// and requestValues re-attaches the request's values. A local mark must
// reach the worker, a remote mark must stay remote, and a context built
// from Background must be remote (the fail-closed default), so a job
// worker never gains the local path exemption by accident.
func TestJobWorker_CarriesTheTransportMark(t *testing.T) {
	if !app.IsRemoteCaller(context.Background()) {
		t.Fatal("a Background-built worker context must be remote (fail closed)")
	}
	for _, tc := range []struct {
		name   string
		mark   func(context.Context) context.Context
		remote bool
		async  bool
	}{
		{"local_sync", app.WithLocalCaller, false, false},
		{"local_async", app.WithLocalCaller, false, true},
		{"rest_sync", func(c context.Context) context.Context { return app.WithRemoteCaller(c, "rest") }, true, false},
		{"unmarked_async", func(c context.Context) context.Context { return c }, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan bool, 1)
			da, _ := jobsAdapter(t, nil, map[string]compositionHandler{
				"media.download": func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
					got <- app.IsRemoteCaller(ctx)
					return json.RawMessage(`{}`), nil
				},
			})
			params := `{}`
			if tc.async {
				params = `{"async":true}`
			}
			if _, err := da.Handle(tc.mark(t.Context()), "media.download", json.RawMessage(params)); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if r := <-got; r != tc.remote {
				t.Fatalf("worker IsRemoteCaller = %v, want %v", r, tc.remote)
			}
		})
	}
}
