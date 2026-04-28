package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
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

// handleConfigFeatures returns the resolved feature-flag state to the CLI.
// Read-only snapshot; feature 017 / FR-100/114/122 / T3-01.
func handleConfigFeatures(flags app.FeatureFlags) func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]any{
			"embeddings":     flags.Embeddings,
			"scheduledSends": flags.ScheduledSends,
			"labels":         flags.Labels,
		})
	}
}

// handlePanic processes the "panic" JSON-RPC method: R-07 full-wipe.
// Delegates to the whatsmeow adapter's Panic entrypoint which logs the
// device out server-side, wipes session.db (+WAL/SHM), messages.db
// (+WAL/SHM), audit.log, and the lockfile, and records AuditPanic.
// Always returns success to the client — recovery intent takes
// precedence over filesystem errors.
func handlePanic(
	waAdapter *wmAdapter.Adapter,
	_ app.SessionStore,
	audit app.AuditLog,
	log *slog.Logger,
) func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if err := waAdapter.Panic(ctx, "rpc"); err != nil {
			log.Error("panic: wipe reported error(s), continuing", "err", err)
		}

		// Mirror the adapter's audit row to the persistent audit.log
		// sink. The adapter's in-memory ring buffer is lost on
		// restart; the slogaudit file is the durable record.
		evt := domain.NewAuditEvent("wa panic", domain.AuditPanic, domain.JID{}, "wiped", "rpc")
		if err := audit.Record(ctx, evt); err != nil {
			log.Error("panic: durable audit record failed", "err", err)
		}

		log.Info("panic completed")

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
