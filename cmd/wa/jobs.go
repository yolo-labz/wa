package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/v2/internal/app"
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
// When ctx ends first (an interrupt) the job is cancelled on the daemon:
// it would otherwise keep running, and for pair keep showing a live QR.
func awaitJob(ctx context.Context, socketPath string, result json.RawMessage, budget time.Duration) (json.RawMessage, int, error) {
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
			return nil, rpcCodeToExit(rpcWaitTimeout), &pollLostError{j.JobID, fmt.Errorf("job %s still %s after %s", j.JobID, j.State, budget)}
		}
		select {
		case <-ctx.Done():
			return nil, 130, &pollLostError{j.JobID, cancelJob(socketPath, j.JobID)}
		case <-time.After(wait):
		}
		next, code, err := callAndClose(socketPath, "jobs.get", map[string]any{"job_id": j.JobID})
		if result = next; err != nil {
			return nil, code, &pollLostError{j.JobID, err}
		}
	}
}

// commandContext is cmd's context; a command run without Execute (a test
// calling RunE) has none, and NotifyContext panics on a nil parent.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// callJob is a job-capable call under an interrupt: the first call, which has
// no job id yet, cancels through its connection, and an answer that is a job
// is followed (and cancelled on interrupt) by awaitJob. ctx must be set up
// before this runs, so an interrupt in the first 10 s is caught too.
func callJob(ctx context.Context, socketPath, method string, params any, budget time.Duration) (json.RawMessage, int, error) {
	result, code, err := callAndCloseCtx(ctx, socketPath, method, params)
	if err != nil {
		return nil, code, err
	}
	out, code, err := awaitJob(ctx, socketPath, result, budget)
	var lost *pollLostError
	if errors.As(err, &lost) && app.IsSendClass(method) {
		// The send was accepted and may still complete: say so, with its id.
		if code == 130 { // an interrupt keeps its own code, and a send is not "cancelled"
			err = fmt.Errorf("interrupted; job %s is still running or finished: check wa jobs get %s; do not resend", lost.id, lost.id)
		} else {
			err = fmt.Errorf("%w; the send may still complete: do not resend; check: wa jobs get %s", err, lost.id)
			code = exitOutcomeUnknown
		}
	}
	return out, code, err
}

// pollLostError is a failure to learn a promoted call's outcome (a lost
// poll, an interrupt, a budget that ran out); the job itself is unaffected.
type pollLostError struct {
	id  string
	err error
}

func (e *pollLostError) Error() string { return e.err.Error() }
func (e *pollLostError) Unwrap() error { return e.err }

var jobsCmd = &cobra.Command{Use: "jobs", Short: "Inspect jobs the daemon is running for you"}

var jobsGetCmd = &cobra.Command{
	Use: "get <job_id>", Short: "Print a job's state and result (use it after a send whose poll was lost)", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, code, err := callAndCloseCtx(commandContext(cmd), flagSocket, "jobs.get", map[string]any{"job_id": args[0]})
		if err != nil {
			return exiterr(code, err)
		}
		fmt.Println(formatResult("jobs.get", result, flagJSON))
		return nil
	},
}

func init() {
	jobsCmd.AddCommand(jobsGetCmd)
	rootCmd.AddCommand(jobsCmd)
}

// cancelJob asks the daemon to stop jobID after an interrupt.
func cancelJob(socketPath, jobID string) error {
	if _, _, err := callAndClose(socketPath, "jobs.cancel", map[string]any{"job_id": jobID}); err != nil {
		return fmt.Errorf("interrupted; jobs.cancel %s failed: %w", jobID, err)
	}
	return fmt.Errorf("interrupted; job %s cancelled", jobID)
}
