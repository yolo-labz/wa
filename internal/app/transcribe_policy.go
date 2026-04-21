package app

// ApprovalMode is the per-contact allowlist decision mode as seen at the
// use-case layer. It mirrors the allowlist.toml `approval` key.
type ApprovalMode uint8

// ApprovalMode values.
const (
	// ApprovalDraft means the contact is unknown or explicitly gated
	// behind human review. Messages are staged as drafts.
	ApprovalDraft ApprovalMode = iota
	// ApprovalManual means sends require per-call confirmation but
	// inbound reads do not. This is the default allowlist tier.
	ApprovalManual
	// ApprovalAuto means the contact has been explicitly trusted to
	// bypass draft review (best friend, self-chat, automation bot).
	ApprovalAuto
)

// TranscribePolicy decides whether a voice-note's transcript should be
// fetched eagerly (on message receipt, paid immediately) or lazily (only
// when a downstream consumer asks for it). Feature 017 CL-02 D: eager for
// contacts the user already trusts enough to auto-approve, lazy for
// everyone else. The `eager` override (set at the use-case boundary) wins
// unconditionally.
//
// This is the only place the decision lives; the inbound message handler
// and the media-resolve path both call it.
func TranscribePolicy(mode ApprovalMode, override bool) bool {
	if override {
		return true
	}
	return mode == ApprovalAuto
}
