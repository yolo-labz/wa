package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// jobPollEvery spaces jobs.get polls; a var so tests can shorten it.
var jobPollEvery = time.Second

type jobAnswer struct {
	JobID  string          `json:"job_id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// waitTimedOut reports the daemon's own wait timeout. -32003 is shared
// with a shutdown refusal, which must end the wait, not be re-polled.
func waitTimedOut(e *rpcError) bool {
	var d struct {
		Reason string `json:"reason"`
	}
	return e.Code == rpcWaitTimeout && json.Unmarshal(e.Data, &d) == nil && d.Reason == "timed_out"
}

// awaitJob turns a job answer (FR-116-16: a slow call returns {job_id,
// state} at 10 s) into the job's own result by polling jobs.get within
// budget, never calling the method again. Other answers pass through.
func awaitJob(socketPath string, result json.RawMessage, budget time.Duration) (json.RawMessage, int, error) {
	deadline := time.Now().Add(budget)
	for {
		var j jobAnswer
		if json.Unmarshal(result, &j) != nil || j.JobID == "" {
			return result, 0, nil
		}
		switch j.State {
		case "done":
			return j.Result, 0, nil
		case "failed", "cancelled", "expired":
			if j.Error != nil {
				return nil, rpcCodeToExit(j.Error.Code), j.Error
			}
			return nil, 1, fmt.Errorf("job %s %s", j.JobID, j.State)
		}
		wait := min(time.Until(deadline), jobPollEvery)
		if wait <= 0 {
			return nil, rpcCodeToExit(rpcWaitTimeout), fmt.Errorf("job %s still %s after %s", j.JobID, j.State, budget)
		}
		<-time.After(wait)
		next, code, err := callAndClose(socketPath, "jobs.get", map[string]any{"job_id": j.JobID})
		if result = next; err != nil {
			return nil, code, err
		}
	}
}
