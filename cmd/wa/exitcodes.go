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
// pins every entry, and TestCatalogCodesAreClassified fails when a code
// reaches internal/agentdocs/errors.json without landing in either
// rpcCodeToExit or deliberatelyGeneric below.
const (
	rpcWaitTimeout              = -32003 // also RequestTimeoutDuringShutdown
	rpcShutdownInProgress       = -32002
	rpcOversizedMessage         = -32004
	rpcBackpressure             = -32001
	rpcUnauthorized             = -32099
	rpcProtocolMismatch         = -32008
	rpcNotPaired                = -32011
	rpcNotAllowlisted           = -32012
	rpcRateLimited              = -32013
	rpcWarmupActive             = -32014
	rpcInvalidJID               = -32015
	rpcMessageTooLarge          = -32016
	rpcNotOnWhatsApp            = -32017
	rpcRecipientMoved           = -32020
	rpcDisconnected             = -32018
	rpcSessionWiped             = -32019
	rpcIdempotencyKeyConflict   = -32108
	rpcDraftState               = -32109
	rpcMediaTooLargeUpload      = -32111
	rpcMessageNotFound          = -32117
	rpcWebhookNotFound          = -32118
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
	case rpcNotPaired, rpcDisconnected, rpcShutdownInProgress,
		// The daemon wiped its own device store and cannot pair again in
		// this process — unusable for the requested work until it is
		// restarted, which is the same remedy class as "daemon is down".
		rpcSessionWiped:
		return exitUnavailable
	case rpcNotAllowlisted, rpcPolicyRefused:
		return exitNotAllowed
	case rpcRateLimited, rpcRateLimitedHard, rpcWarmupActive, rpcWaitTimeout,
		// Backpressure is a "the daemon is shedding load, back off" refusal:
		// same caller action as a rate cap, so it shares the bucket.
		rpcBackpressure:
		return exitRateLimited
	case rpcInvalidJID, rpcMessageTooLarge, rpcMediaTooLarge, rpcOversizedMessage,
		rpcInvalidParams, rpcMethodNotFound, rpcScheduleInPast,
		rpcIdempotencyCollision, rpcUnsupportedMessageType,
		// The recipient/message/draft/webhook the caller named does not exist
		// or is in the wrong state — bad input, same class as a bad JID.
		// The number is registered, just not under the JID the caller
		// named — same remedy as any other wrong recipient: fix the
		// argument and resend.
		rpcNotOnWhatsApp, rpcRecipientMoved, rpcMessageNotFound, rpcDraftState, rpcWebhookNotFound,
		rpcMediaTooLargeUpload, rpcIdempotencyKeyConflict,
		// A missing or expired bearer token on a --remote call. 64 rather than
		// 78 to match the sibling raw-HTTP media path, which already exits 64
		// on HTTP 401 (fetchMediaHTTP in cmd_media.go) — one answer for one
		// user-visible failure across both remote transports.
		rpcUnauthorized:
		return exitUsage
	case rpcEmbeddingsDisabled, rpcLabelsUnsupported, rpcTranscriberNotConfigured,
		// Version skew between `wa` and `wad`: the install is misconfigured,
		// nothing about the command itself is wrong.
		rpcProtocolMismatch:
		return exitConfig
	default:
		return exitGeneric
	}
}

// deliberatelyGeneric lists the catalogued daemon error codes that stay at
// exitGeneric on purpose, each with the reason. TestCatalogCodesAreClassified
// treats membership here as a decision; a code in neither this map nor
// rpcCodeToExit is an undecided one, and fails the build.
var deliberatelyGeneric = map[int]string{
	-32700: "parse_error: the CLI emitted malformed JSON — a bug in wa, not something the caller can act on",
	-32600: "invalid_request: same, a malformed frame from wa itself",
	-32603: "internal_error: a daemon fault with no caller-side remedy; wad.log carries the detail",
	-32000: "peer_cred_rejected: a shared wire slot — the dispatcher reuses it for upstream_error " +
		"(internal/adapters/primary/socket/dispatch.go), so the code alone does not identify the class",
	-32116: "transcribe_failed: a runtime backend failure with no sysexits home — the download " +
		"itself succeeded, so the hint carries the remedy instead of the exit code",
	-32301: "media_not_cached: likewise runtime; the hint points at `wa migrate` / a re-sync",
	-32005: "subscription_closed: a stream frame, not an RPC response; cmd_subscribe.go exits 0 on it",
	-32006: "pong_timeout: a stream frame; cmd_subscribe.go exits 12 on it",
	-32007: "stream_drop: a stream frame; cmd_subscribe.go keeps reading after it",
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
	rpcSessionWiped:             "this daemon wiped its own session; restart wad, then run `wa pair`",
	rpcPolicyRefused:            "refused by policy — check the allowlist action/scope or the edit window",
	rpcScheduleInPast:           "schedule fire-at must be in the future",
	rpcEmbeddingsDisabled:       "embeddings are off; start wad with an embedder configured",
	rpcLabelsUnsupported:        "labels require a WhatsApp Business session",
	rpcTranscriberNotConfigured: "set WA_TRANSCRIBER=whispercpp|hear|groq on the daemon",
	rpcTranscribeFailed:         "transcription failed; the media still downloaded — check the transcriber backend",
	rpcMediaTooLarge:            "media exceeds the 16 MiB cap",
	rpcUnsupportedMessageType:   "that message has no downloadable media",
	rpcMediaNotCached:           "raw message not in the store; try `wa migrate` or re-sync the chat",
	rpcNotOnWhatsApp:            "that number has no WhatsApp account; the pre-send deliverability gate rejected it",
	rpcRecipientMoved:           "that number is registered under a different JID (the error names it); resend to the one the server routes",
	rpcMessageNotFound:          "no such message id; list them with `wa thread get <chat>` or `wa messages list`",
	rpcDraftState:               "the draft is not in a state that allows this; check `wa draft get <id>`",
	rpcWebhookNotFound:          "no such endpoint id; list them with `wa webhook list`",
	rpcMediaTooLargeUpload:      "media exceeds the 16 MiB cap",
	rpcIdempotencyKeyConflict:   "that idempotency key was used with a different payload; reuse the original or mint a new key",
	rpcUnauthorized:             "remote auth failed; set a valid $WA_TOKEN for --remote calls",
	rpcBackpressure:             "the daemon is shedding load; back off and retry",
	rpcProtocolMismatch:         "`wa` and `wad` speak different protocol versions; upgrade whichever is older",
}

// hintForRPCCode returns a short, actionable remediation line for a daemon
// refusal, or "" if none applies. run() prints it to stderr (prefixed
// "hint:") after a failed command, extending the actionable-diagnostics
// ethos of `wa doctor` to every command.
func hintForRPCCode(code int) string {
	return rpcHints[code]
}
