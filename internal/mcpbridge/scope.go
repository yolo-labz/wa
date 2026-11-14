package mcpbridge

// Token scopes, ordered read < send < admin, matching the daemon's
// rest.MethodScope ladder.
const (
	ScopeRead  = "read"
	ScopeSend  = "send"
	ScopeAdmin = "admin"
)

var scopeRank = map[string]int{ScopeRead: 1, ScopeSend: 2, ScopeAdmin: 3}

// toolScopes is each tool's required scope: the highest rest.RequiredScope
// of the daemon methods it calls (cmd/wad TestMCPScope_ToolListIsFunctionOfScope
// holds the two tables together). addTool refuses a tool missing here.
// No tool reaches an allowlist, pairing, session or token mutation at any
// scope (SR-116-09): those stay human-only, in /admin or the CLI.
var toolScopes = map[string]string{
	"wa_search_messages":  ScopeRead,
	"wa_get_thread":       ScopeRead,
	"wa_transcribe_voice": ScopeRead,
	"wa_resolve_contact":  ScopeRead,
	"wa_list_chats":       ScopeRead,
	"wa_draft_review":     ScopeRead,
	"wa_allowlist_check":  ScopeRead,
	"wa_list_media":       ScopeRead,
	"wa_get_media":        ScopeRead,
	"wa_group_info":       ScopeRead,
	"wa_status":           ScopeRead,
	"wa_job_status":       ScopeRead,
	"wa_sync_history":     ScopeSend,
	"wa_send_message":     ScopeSend,
	"wa_send_media":       ScopeSend,
	"wa_wait_for_reply":   ScopeSend,
	"wa_schedule_message": ScopeAdmin,
}
