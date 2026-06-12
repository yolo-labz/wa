package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// reloadResult is the admin.reload JSON-RPC result.
type reloadResult struct {
	Schema   string `json:"schema"`
	Entries  int    `json:"entries"`
	SinceMs  int64  `json:"reloadMs"`
	Allowlst string `json:"path"`
}

// handleAdminReload returns a compositionHandler for admin.reload
// (FR-038). Delegates to reloadAllowlist — the same swap + audit path
// the fsnotify/SIGHUP watcher uses — so operators see one code path
// regardless of trigger (SF-05). A parse failure additionally returns
// the RPC error to the caller, on top of the refused AuditReload row
// the shared path records.
func handleAdminReload(
	allowlist *domain.Allowlist,
	allowlistMu *sync.RWMutex,
	allowlistPath string,
	audit app.AuditLog,
	log *slog.Logger,
) compositionHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		start := time.Now()

		entries, err := reloadAllowlist(
			ctx, "internal", allowlistPath, allowlist, allowlistMu, audit, log)
		if err != nil {
			return nil, fmt.Errorf("allowlist reload: %w", err)
		}

		elapsed := time.Since(start).Milliseconds()

		return json.Marshal(reloadResult{
			Schema:   "wa.admin.reload/v1",
			Entries:  entries,
			SinceMs:  elapsed,
			Allowlst: allowlistPath,
		})
	}
}
