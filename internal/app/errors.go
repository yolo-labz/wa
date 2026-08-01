package app

import "errors"

// codedError is the interface typed errors implement so the socket adapter
// can map them to JSON-RPC error codes (FR-039).
type codedError interface {
	RPCCode() int
}

// rpcErr is the concrete typed error. Each sentinel wraps a base error
// so errors.Is works against the sentinel AND the base.
type rpcErr struct {
	code int
	msg  string
	base error
}

func (e *rpcErr) Error() string { return e.msg }
func (e *rpcErr) RPCCode() int  { return e.code }
func (e *rpcErr) Unwrap() error { return e.base }

// newRPCErr creates a typed rpc error with a distinct base sentinel
// and registers it in the catalog (feature 111: docs/errors.json drift
// guard — every code the app layer can emit must appear in the
// published machine-readable catalog).
func newRPCErr(code int, msg string) *rpcErr {
	base := errors.New(msg)
	rpcErrCatalog = append(rpcErrCatalog, RPCErrEntry{Code: code, Message: msg})
	return &rpcErr{code: code, msg: msg, base: base}
}

// InvalidParams returns an ErrInvalidParams-coded error carrying a detail
// message: errors.Is(err, ErrInvalidParams) holds, and because the socket
// layer puts a coded error's own text on the wire, the caller reads WHICH
// parameter it got wrong instead of a bare "invalid params".
//
// Not registered in the catalog — the -32602 row is already there under
// ErrInvalidParams, and the catalog enumerates codes, not messages.
func InvalidParams(detail string) error {
	return &rpcErr{code: -32602, msg: "invalid params: " + detail, base: ErrInvalidParams}
}

// RPCErrEntry is one row of the app-layer error catalog.
type RPCErrEntry struct {
	Code    int
	Message string
}

// rpcErrCatalog accumulates every newRPCErr registration at init time.
var rpcErrCatalog []RPCErrEntry

// RPCErrorCatalog returns the app-layer typed-error catalog. Test-time
// drift guard input for docs/errors.json; stable order not guaranteed.
func RPCErrorCatalog() []RPCErrEntry { return rpcErrCatalog }

// Typed errors — codes from data-model.md §Typed errors and spec FR-039.
var (
	ErrNotPaired      = newRPCErr(-32011, "not paired")
	ErrNotAllowlisted = newRPCErr(-32012, "not allowlisted")
	ErrRateLimited    = newRPCErr(-32013, "rate limited")
	// ErrRateLimitedHard is feature 018's daily-cap refusal for adapter-
	// side hard limits (≤5 groups/day Create, ≤50 participants/day Add).
	// Code -32200 matches the frozen v2 wire protocol slot "rate_limited
	// or media_too_large" (contracts/jsonrpc-v2.json:375). Distinct from
	// ErrRateLimited (-32013) which remains the pre-018 send-path refusal.
	ErrRateLimitedHard = newRPCErr(-32200, "rate limited")
	ErrWarmupActive    = newRPCErr(-32014, "warmup active")
	ErrInvalidJID      = newRPCErr(-32015, "invalid JID")
	ErrMessageTooLarge = newRPCErr(-32016, "message too large")
	// ErrNotOnWhatsApp is returned by the send path when the recipient
	// phone number has no WhatsApp account (pre-send IsOnWhatsApp gate).
	// Without it a send to a fabricated/scraped number was silently
	// accepted (whatsmeow mints a local message id) and never delivered —
	// the agent saw "Sent: <id>" and wrongly believed it landed.
	ErrNotOnWhatsApp = newRPCErr(-32017, "recipient not on WhatsApp")
	ErrDisconnected  = newRPCErr(-32018, "disconnected")
	// ErrSessionWiped is returned by pair when the daemon destroyed its own
	// device store earlier in this process (`wa panic`, or the
	// `session.logout` that `wa pair --reset` issues first) and cannot
	// complete another handshake without a restart. Distinct from
	// ErrNotPaired, which means the opposite — a session is present and must
	// be unlinked first. Before it existed, pairing against a wiped store
	// returned success and paired nothing. Issue #310.
	ErrSessionWiped = newRPCErr(-32019, "session wiped; restart the daemon before pairing")
	// ErrScheduleInPast is returned when schedule.send/update is called with
	// a fire_at timestamp that is not strictly after now. Feature 017 T3-18.
	ErrScheduleInPast = newRPCErr(-32112, "schedule in past")
	// ErrEmbeddingsDisabled is returned when messages.search is called with
	// mode=vector while the embeddings feature flag is off. Feature 017 T3-12.
	ErrEmbeddingsDisabled = newRPCErr(-32113, "embeddings disabled")
	// ErrLabelsUnsupported is returned when labels.* is called without a
	// WA Business session or with the labels feature flag off. Feature 017 T3-22.
	ErrLabelsUnsupported = newRPCErr(-32114, "labels unsupported")
	// ErrTranscriberNotConfigured is returned by media.download when params
	// request transcription (transcribe=true on an audio/* mime) but the
	// daemon was started without WA_TRANSCRIBER selecting an adapter.
	// Spec 110h FR-002 — distinct from method_not_found so callers can
	// distinguish "transcription is wholly unavailable" from "method is
	// disabled". Operator action: set WA_TRANSCRIBER=whispercpp|hear|groq.
	ErrTranscriberNotConfigured = newRPCErr(-32115, "transcriber not configured")
	// ErrTranscribeFailed is returned when the selected Transcriber adapter
	// errored (binary missing, network failure, model load failure). Spec
	// 110h FR-008 — never silently degrades to an empty transcript; the
	// download itself still succeeds and the payload remains on disk,
	// only the transcript step is reported as failed.
	ErrTranscribeFailed = newRPCErr(-32116, "transcribe failed")
	ErrWaitTimeout      = newRPCErr(-32003, "wait timeout")
	ErrMethodNotFound   = newRPCErr(-32601, "method not found")
	ErrInvalidParams    = newRPCErr(-32602, "invalid params")
	// ErrNotIdentity is returned by IdentityResolver methods when the
	// supplied JID is not of the expected kind (PN for ResolveLID, LID
	// for ResolvePN). Maps to -32015 InvalidJID at the socket boundary —
	// the input failed the structural precondition. Spec 106.
	ErrNotIdentity = newRPCErr(-32015, "JID is not an identity (PN or LID)")
)

// IsCodedError reports whether err implements the codedError interface
// and, if so, returns the RPC code. This is the single callsite for the
// socket adapter's error-to-JSON-RPC mapping.
//
// Note: codedError is a capability interface (RPCCode() only), not an
// error interface, so errors.AsType[codedError] is not applicable —
// errors.AsType requires E to satisfy error. errors.As is correct here.
func IsCodedError(err error) (int, bool) {
	var ce codedError
	if errors.As(err, &ce) {
		return ce.RPCCode(), true
	}
	return 0, false
}

// Error aggregation note (FR-017): use errors.Join for combining
// independent errors (cleanup, validation, parallel fan-out). Single-%w
// via fmt.Errorf("context: %w", err) is for adding call-site context.
// GOTCHA: errors.Unwrap() returns nil for errors.Join results — always
// use errors.Is / errors.As (which traverse the tree correctly).
// See: golang/go#57358, Ian Lewis TIL (March 2025).
