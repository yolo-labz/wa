package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// reloadResult is the admin.reload JSON-RPC result.
type reloadResult struct {
	Schema   string `json:"schema"`
	Entries  int    `json:"entries"`
	SinceMs  int64  `json:"reloadMs"`
	Allowlst string `json:"path"`
}

// handleAdminReload returns a compositionHandler for admin.reload
// (FR-038). Re-reads allowlist.toml, atomically swaps in-memory state
// under the existing RWMutex, and records an AuditReload entry. This is
// the same reload logic used by the SIGHUP path in watchAllowlist — both
// converge on the identical atomic swap + audit emit so operators see
// one code path regardless of trigger.
func handleAdminReload(
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
	allowlistPath string,
	audit app.AuditLog,
	log *slog.Logger,
) compositionHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		start := time.Now()

		newAL, err := loadAllowlist(allowlistPath)
		if err != nil {
			log.Error("admin.reload failed", "err", err)
			return nil, fmt.Errorf("allowlist reload: %w", err)
		}

		allowlistMu.Lock()
		for jid, actions := range allowlist.Entries() {
			allowlist.Revoke(jid, actions...)
		}
		for jid, actions := range newAL.Entries() {
			allowlist.Grant(jid, actions...)
		}
		entries := allowlist.Size()
		allowlistMu.Unlock()

		elapsed := time.Since(start).Milliseconds()
		log.Info("allowlist reloaded via admin.reload", "entries", entries, "ms", elapsed)

		if err := audit.Record(ctx, domain.NewAuditEvent(
			"wad",
			domain.AuditReload,
			domain.JID{},
			"granted",
			fmt.Sprintf("allowlist reloaded: %d entries", entries),
		)); err != nil {
			log.Warn("audit.reload record failed", "err", err)
		}

		return json.Marshal(reloadResult{
			Schema:   "wa.admin.reload/v1",
			Entries:  entries,
			SinceMs:  elapsed,
			Allowlst: allowlistPath,
		})
	}
}
