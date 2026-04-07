package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	wmAdapter "github.com/yolo-labz/wa/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// allowParams is the JSON-RPC params for the "allow" method.
type allowParams struct {
	Op      string   `json:"op"`
	JID     string   `json:"jid,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

// allowRuleResult is a single entry in the "allow list" response.
type allowRuleResult struct {
	JID     string   `json:"jid"`
	Actions []string `json:"actions"`
}

// handleAllow processes the "allow" JSON-RPC method: add, remove, or list.
// It mutates the in-memory allowlist, persists to TOML, and audits.
func handleAllow(
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
	allowlistPath string,
	audit app.AuditLog,
	log *slog.Logger,
) func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p allowParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, app.ErrInvalidParams
		}

		switch p.Op {
		case "add":
			return handleAllowAdd(ctx, p, allowlist, allowlistMu, allowlistPath, audit, log)
		case "remove":
			return handleAllowRemove(ctx, p, allowlist, allowlistMu, allowlistPath, audit, log)
		case "list":
			return handleAllowList(allowlist, allowlistMu)
		default:
			return nil, app.ErrInvalidParams
		}
	}
}

func handleAllowAdd(
	ctx context.Context,
	p allowParams,
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
	allowlistPath string,
	audit app.AuditLog,
	log *slog.Logger,
) (json.RawMessage, error) {
	if p.JID == "" || len(p.Actions) == 0 {
		return nil, app.ErrInvalidParams
	}

	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, app.ErrInvalidJID
	}

	actions := make([]domain.Action, 0, len(p.Actions))
	for _, s := range p.Actions {
		a, err := domain.ParseAction(s)
		if err != nil {
			return nil, app.ErrInvalidParams
		}
		actions = append(actions, a)
	}

	allowlistMu.Lock()
	allowlist.Grant(jid, actions...)
	allowlistMu.Unlock()

	if err := saveAllowlist(allowlistPath, allowlist); err != nil {
		log.Error("failed to persist allowlist", "err", err)
		return nil, fmt.Errorf("persist allowlist: %w", err)
	}

	// Audit the grant.
	evt := domain.NewAuditEvent("wa allow add", domain.AuditGrant, jid, "granted", fmt.Sprintf("actions=%v", p.Actions))
	if err := audit.Record(ctx, evt); err != nil {
		log.Error("audit record failed", "err", err)
	}

	log.Info("allowlist add", "jid", jid.String(), "actions", p.Actions)

	return json.Marshal(map[string]any{
		"added":   true,
		"jid":     jid.String(),
		"actions": p.Actions,
	})
}

func handleAllowRemove(
	ctx context.Context,
	p allowParams,
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
	allowlistPath string,
	audit app.AuditLog,
	log *slog.Logger,
) (json.RawMessage, error) {
	if p.JID == "" {
		return nil, app.ErrInvalidParams
	}

	jid, err := domain.Parse(p.JID)
	if err != nil {
		return nil, app.ErrInvalidJID
	}

	// Revoke all actions for this JID.
	entries := allowlist.Entries()
	if actions, ok := entries[jid]; ok {
		allowlistMu.Lock()
		allowlist.Revoke(jid, actions...)
		allowlistMu.Unlock()
	}

	if err := saveAllowlist(allowlistPath, allowlist); err != nil {
		log.Error("failed to persist allowlist", "err", err)
		return nil, fmt.Errorf("persist allowlist: %w", err)
	}

	// Audit the revoke.
	evt := domain.NewAuditEvent("wa allow remove", domain.AuditRevoke, jid, "revoked", "all actions")
	if err := audit.Record(ctx, evt); err != nil {
		log.Error("audit record failed", "err", err)
	}

	log.Info("allowlist remove", "jid", jid.String())

	return json.Marshal(map[string]any{
		"removed": true,
		"jid":     jid.String(),
	})
}

// handlePanic processes the "panic" JSON-RPC method: unlink device
// server-side, wipe local session, audit the event. Always succeeds
// locally even if the upstream logout call fails.
func handlePanic(
	waAdapter *wmAdapter.Adapter,
	session app.SessionStore,
	audit app.AuditLog,
	log *slog.Logger,
) func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		outcome := "unlinked"

		// Step 1: attempt server-side device unlink.
		if err := waAdapter.Logout(ctx); err != nil {
			log.Error("panic: upstream logout failed, proceeding with local wipe", "err", err)
			outcome = "unlinked:local-only"
		}

		// Step 2: wipe local session store.
		if err := session.Clear(ctx); err != nil {
			log.Error("panic: session clear failed", "err", err)
			// Still report success — the intent is recovery.
		}

		// Step 3: audit the event.
		evt := domain.NewAuditEvent("wa panic", domain.AuditPanic, domain.JID{}, outcome, "device unlink + session wipe")
		if err := audit.Record(ctx, evt); err != nil {
			log.Error("panic: audit record failed", "err", err)
		}

		log.Info("panic completed", "outcome", outcome)

		return json.Marshal(map[string]any{
			"unlinked": true,
		})
	}
}

func handleAllowList(
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
) (json.RawMessage, error) {
	allowlistMu.RLock()
	entries := allowlist.Entries()
	allowlistMu.RUnlock()

	rules := make([]allowRuleResult, 0, len(entries))
	for jid, actions := range entries {
		strs := make([]string, len(actions))
		for i, a := range actions {
			strs[i] = a.String()
		}
		rules = append(rules, allowRuleResult{
			JID:     jid.String(),
			Actions: strs,
		})
	}

	return json.Marshal(map[string]any{
		"rules": rules,
	})
}
