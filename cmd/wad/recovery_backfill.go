package main

import (
	"log/slog"
	"os"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
)

// wireRecoveryBackfill connects FR-116-24/25: sync.force reads recovery
// evidence for gap reachability, and each episode's first live anchor feeds
// the deferred puller. Scheduling is opt-in via
// WAD_STORAGE_RECOVERY_BACKFILL=1; otherwise the daemon is detect-only.
func wireRecoveryBackfill(a *wmAdapter.Adapter, h *sqlitehistory.Store, log *slog.Logger) *wmAdapter.RecoveryBackfill {
	if h == nil {
		return nil
	}
	a.SetRecoveryEvidence(h)
	v := os.Getenv("WAD_STORAGE_RECOVERY_BACKFILL")
	enabled := v == "1" || v == "true"
	w := wmAdapter.NewRecoveryBackfill(a, h.RecordRecoveryPull, enabled)
	h.SetRecoveryAnchorHook(w.OnAnchor)
	log.Info("storage recovery backfill", "scheduling", enabled)
	return w
}
