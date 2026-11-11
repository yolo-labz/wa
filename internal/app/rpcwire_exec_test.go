package app_test

import (
	"fmt"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRPCWire_ExecutorErrors pins the wire row of every executor refusal
// (FR-116-16), wrapped as transports see them. A foreign job id reads
// exactly like an unknown one, and a recovered panic carries no detail.
func TestRPCWire_ExecutorErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err    error
		code   int32
		reason domain.ErrorReason
		msg    string // exact, when the text is part of the contract
	}{
		{app.ErrExecCapacity, -32001, domain.ReasonCapacityExceeded, ""},
		{app.ErrJobNotFound, -32021, domain.ReasonJobNotFound, "job not found"},
		{app.ErrExecDeadline, -32022, domain.ReasonTimedOut, ""},
		{app.ErrAsyncNotJob, -32602, domain.ReasonInvalidParams, ""},
		{app.ErrUnclassifiedMethod, -32602, domain.ReasonInvalidParams, ""},
		{app.ErrPrincipalInvalid, -32099, domain.ReasonScopeInsufficient, "unauthorized"},
		{app.ErrInternal, -32603, domain.ReasonInternal, app.MsgInternalError},
	} {
		code, msg, reason := app.RPCWire(fmt.Errorf("job 0a1b of tok_x: %w", c.err))
		if code != c.code || reason != c.reason || (c.msg != "" && msg != c.msg) {
			t.Errorf("%v → (%d, %q, %s), want (%d, %q, %s)", c.err, code, msg, reason, c.code, c.msg, c.reason)
		}
	}
}
