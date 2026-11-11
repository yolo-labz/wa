package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"golang.org/x/time/rate"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// allowCheckActions are the actions an allow.check may ask about.
var allowCheckActions = map[string]domain.Action{
	"send":         domain.ActionSend,
	"read":         domain.ActionRead,
	"group.add":    domain.ActionGroupAdd,
	"group.create": domain.ActionGroupCreate,
}

// allowCheckResult answers one (recipient, action) pair and nothing else:
// no other action or recipient is revealed (FR-116-14, FR-050).
type allowCheckResult struct {
	Allowed     bool   `json:"allowed"`
	Action      string `json:"action"`
	Remediation string `json:"remediation,omitempty"`
}

// newAllowCheckLimiter bounds allow.check probing.
// ponytail: the daemon has no shared read-surface limiter yet, so this
// method carries its own; fold it in when one exists.
func newAllowCheckLimiter() *rate.Limiter { return rate.NewLimiter(10, 20) }

// handleAllowCheck is the read-scope allow.check method: it reads the
// in-memory allowlist without mutating it and audits every decision. An
// audit failure fails the check closed.
func handleAllowCheck(allowlist *domain.Allowlist, mu *sync.RWMutex, audit app.AuditLog, limiter *rate.Limiter) compositionHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p struct {
			JID    string `json:"jid"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, app.InvalidParams("allow.check params must be {jid, action}")
		}
		action, ok := allowCheckActions[p.Action]
		if !ok {
			return nil, app.InvalidParams("action must be one of send, read, group.add, group.create")
		}
		jid, err := domain.Parse(p.JID)
		if err != nil {
			return nil, app.InvalidParams("jid must be a phone number or WhatsApp JID")
		}
		if !limiter.Allow() {
			return nil, app.ErrRateLimited
		}
		mu.RLock()
		allowed := allowlist.Allows(jid, action)
		mu.RUnlock()
		res := allowCheckResult{Allowed: allowed, Action: p.Action}
		decision := "allowed"
		if !allowed {
			decision = "denied"
			res.Remediation = mcpbridge.Remediation(domain.ReasonNotAllowlisted)
		}
		evt := domain.NewAuditEvent("allow.check", domain.AuditAllowCheck, jid, decision, "action="+p.Action)
		if err := audit.Record(ctx, evt); err != nil {
			return nil, fmt.Errorf("audit allow.check: %w", err)
		}
		return json.Marshal(res)
	}
}
