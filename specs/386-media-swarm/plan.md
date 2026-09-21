# Plan

1. Fix the shared `scripts/record-demo.sh`: atomic per-checkout output lock,
   short mktemp sandbox, cleanup on exit/signals, sanitized runtime environment,
   staging before publication. Reject a fixed path plus delete (data loss),
   and reject concurrent writes to the same media outputs (corruption).
2. Add a small stdlib Python subprocess regression, using fake build tools only
   for lifecycle tests; actual binaries are mandatory for the media recording.
3. Update the existing tape with isolated environment sourcing, MP4/WebM output,
   readable pacing and no mutations. Keep 1x playback, no invented output.
4. Generate PNG and actual CLI text; verify streams, budgets, durations and
   inspect frames. Update README and canonical report with executed evidence.

No daemon/Go behavior changes, new dependencies, workflows or deployment.
No schema/data-model additions. Current CLI surface was inspected before plan.
Requirement/plan consistency: every requirement maps to a task below; no open
clarifications. The unavailable legacy model-pinned workflow is not executed.
