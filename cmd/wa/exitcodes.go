package main

import "fmt"

// exitError wraps an error with a specific exit code. main() checks for
// this type to set the process exit code without calling os.Exit inside
// command handlers (which kills test processes). Pattern from gh CLI.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func (e *exitError) ExitCode() int { return e.code }

// exitf creates an exitError with a formatted message.
func exitf(code int, format string, args ...any) *exitError {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

// exiterr creates an exitError wrapping an existing error.
func exiterr(code int, err error) *exitError {
	return &exitError{code: code, err: err}
}

// CLI exit codes — the sysexits.h subset documented in docs/manual.md §8.
const (
	exitGeneric     = 1  // unexpected runtime error
	exitUnavailable = 10 // daemon not running / not connected / shutting down
	exitNotAllowed  = 11 // refused by allowlist or policy
	exitRateLimited = 12 // rate / warmup / wait cap
	exitUsage       = 64 // bad flags, JID, params, or out-of-range input
	exitConfig      = 78 // feature disabled / misconfigured daemon
)

// JSON-RPC error codes emitted by the wad daemon. The canonical
// definitions live daemon-side in internal/app/errors.go and
// internal/adapters/primary/socket/errcodes.go; the CLI is a separate
// `package main` and cannot import them, so they are mirrored here to keep
// the exit-code and hint mappings self-documenting. TestRPCCodeToExit
// pins every entry, guarding this mirror against drift.
const (
	rpcWaitTimeout              = -32003 // also RequestTimeoutDuringShutdown
	rpcShutdownInProgress       = -32002
	rpcOversizedMessage         = -32004
	rpcNotPaired                = -32011
	rpcNotAllowlisted           = -32012
	rpcRateLimited              = -32013
	rpcWarmupActive             = -32014
	rpcInvalidJID               = -32015
	rpcMessageTooLarge          = -32016
	rpcDisconnected             = -32018
	rpcPolicyRefused            = -32100
	rpcIdempotencyCollision     = -32101
	rpcScheduleInPast           = -32112
	rpcEmbeddingsDisabled       = -32113
	rpcLabelsUnsupported        = -32114
	rpcTranscriberNotConfigured = -32115
	rpcTranscribeFailed         = -32116
	rpcRateLimitedHard          = -32200
	rpcMediaTooLarge            = -32201
	rpcUnsupportedMessageType   = -32300
	rpcMediaNotCached           = -32301
	rpcMethodNotFound           = -32601
	rpcInvalidParams            = -32602
)

// rpcCodeToExit maps a JSON-RPC error code to a CLI exit code per
// docs/manual.md §8. The §8 contract has seven buckets, so codes without a
// precise sysexits home fall to the closest class: policy/permission
// refusals → 11, bad-input/out-of-range → 64, feature-off/misconfig → 78,
// daemon-down/disconnected/draining → 10. Genuinely-runtime failures
// (transcribe failed, media-not-cached, internal error) stay 1 — the hint
// (hintForRPCCode) keeps them actionable even at the generic code.
func rpcCodeToExit(code int) int {
	switch code {
	case rpcNotPaired, rpcDisconnected, rpcShutdownInProgress:
		return exitUnavailable
	case rpcNotAllowlisted, rpcPolicyRefused:
		return exitNotAllowed
	case rpcRateLimited, rpcRateLimitedHard, rpcWarmupActive, rpcWaitTimeout:
		return exitRateLimited
	case rpcInvalidJID, rpcMessageTooLarge, rpcMediaTooLarge, rpcOversizedMessage,
		rpcInvalidParams, rpcMethodNotFound, rpcScheduleInPast,
		rpcIdempotencyCollision, rpcUnsupportedMessageType:
		return exitUsage
	case rpcEmbeddingsDisabled, rpcLabelsUnsupported, rpcTranscriberNotConfigured:
		return exitConfig
	default:
		return exitGeneric
	}
}

// rpcHints maps a daemon refusal code to a short, actionable remediation
// line. A data table (not a switch) so adding a hint is a one-line edit and
// the lookup stays trivial.
var rpcHints = map[int]string{
	rpcNotPaired:                "link a device first: `wa pair`",
	rpcNotAllowlisted:           "grant access: `wa allow add <jid> --actions send`",
	rpcRateLimited:              "rate cap hit (per-second/minute/day); retry later",
	rpcRateLimitedHard:          "rate cap hit (per-second/minute/day); retry later",
	rpcWarmupActive:             "new-session warmup ramp active; sending opens up gradually",
	rpcInvalidJID:               "JID format: 5511999999999@s.whatsapp.net (or a group / @lid JID)",
	rpcDisconnected:             "daemon is not connected to WhatsApp; check `wa status` / `wa doctor`",
	rpcPolicyRefused:            "refused by policy — check the allowlist action/scope or the edit window",
	rpcScheduleInPast:           "schedule fire-at must be in the future",
	rpcEmbeddingsDisabled:       "embeddings are off; start wad with an embedder configured",
	rpcLabelsUnsupported:        "labels require a WhatsApp Business session",
	rpcTranscriberNotConfigured: "set WA_TRANSCRIBER=whispercpp|hear|groq on the daemon",
	rpcTranscribeFailed:         "transcription failed; the media still downloaded — check the transcriber backend",
	rpcMediaTooLarge:            "media exceeds the 16 MiB cap",
	rpcUnsupportedMessageType:   "that message has no downloadable media",
	rpcMediaNotCached:           "raw message not in the store; try `wa migrate` or re-sync the chat",
}

// hintForRPCCode returns a short, actionable remediation line for a daemon
// refusal, or "" if none applies. run() prints it to stderr (prefixed
// "hint:") after a failed command, extending the actionable-diagnostics
// ethos of `wa doctor` to every command.
func hintForRPCCode(code int) string {
	return rpcHints[code]
}
