package mcpbridge

import "fmt"

// remediate converts a daemon JSON-RPC refusal into an instructive
// tool-execution error (SEP-1303). The agent reads this text — every
// branch must say what happened AND what action unblocks it, naming
// the operator command where one exists. Code bands per docs/manual.md
// §7: -32000.. upstream, -32100.. policy, -32200.. rate-limit.
func remediate(e *RPCError) error {
	switch {
	case e.Code >= -32199 && e.Code <= -32100:
		return fmt.Errorf("policy refusal (%d): %s. The recipient is not allowlisted for this action; "+
			"the human operator can grant it with `wa allow add <jid> --actions send`. "+
			"Do not retry until they confirm", e.Code, e.Message)
	case e.Code >= -32299 && e.Code <= -32200:
		return fmt.Errorf("rate limited (%d): %s. The daemon enforces non-overridable send budgets "+
			"(per-second/minute/day, with a warmup ramp on fresh sessions). Wait before retrying; "+
			"do not attempt to bypass", e.Code, e.Message)
	case e.Code == -32601:
		return fmt.Errorf("method not available (%d): %s. The daemon may be an older version or the "+
			"feature is flag-gated in config.toml — ask the operator to upgrade wad or enable the flag", e.Code, e.Message)
	case e.Code >= -32099 && e.Code <= -32000:
		return fmt.Errorf("WhatsApp upstream error (%d): %s. This is a protocol-side failure, not a "+
			"policy refusal — check `wa status` for connectivity before retrying once", e.Code, e.Message)
	default:
		return fmt.Errorf("daemon error (%d): %s", e.Code, e.Message)
	}
}
