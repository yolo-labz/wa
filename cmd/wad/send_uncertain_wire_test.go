package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// The id of a possibly-delivered message reaches the caller on every wad
// transport: /mcp as error.data.message_id, and jobs.get as error.message_id.
func TestSendUncertain_IDReachesMCPHTTPAndJobsGet(t *testing.T) {
	uncertain := errors.New("x")
	uncertain = domain.NewSendUncertain("WAID-9", uncertain)

	_, rpcErr, _ := dispatcherCaller(&stubRESTDispatcher{err: uncertain}, rest.ScopeAdminStr)(context.Background(), "send", nil)
	var data domain.ErrorData
	if rpcErr == nil || rpcErr.Code != -32023 || json.Unmarshal(rpcErr.Data, &data) != nil || data.MessageID != "WAID-9" || data.Reason != domain.ReasonSendUncertain {
		t.Fatalf("/mcp: %+v data %+v, want -32023 send_uncertain with message_id WAID-9", rpcErr, data)
	}

	synctest.Test(t, func(t *testing.T) {
		da, _ := jobsAdapter(t, nil, map[string]compositionHandler{
			"media.download": func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, uncertain },
		})
		h := socketCaller{da, "default"}
		job := mustRPC(t, h, "media.download", `{"async":true}`)
		synctest.Wait()
		got := mustRPC(t, h, "jobs.get", jobArg(job["job_id"]))
		e, _ := got["error"].(map[string]any)
		if e == nil || e["message_id"] != "WAID-9" || e["reason"] != string(domain.ReasonSendUncertain) {
			t.Fatalf("jobs.get = %v, want error.message_id WAID-9 with reason send_uncertain", got)
		}
	})
}
