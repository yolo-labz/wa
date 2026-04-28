package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// auditRotateResult is the admin.audit.rotate JSON-RPC result.
type auditRotateResult struct {
	Schema       string `json:"schema"`
	RotatedPath  string `json:"rotatedPath"`
	RotatedBytes int64  `json:"rotatedBytes"`
	NewPath      string `json:"newPath"`
}

// handleAdminAuditRotate returns a compositionHandler for
// admin.audit.rotate (FR-038). Atomically renames the live audit log
// to a timestamped sibling and opens a fresh 0600 file in its place.
func handleAdminAuditRotate(audit *slogaudit.Audit, log *slog.Logger) compositionHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		rotated, err := audit.Rotate()
		if err != nil {
			log.Error("audit rotate failed", "err", err)
			return nil, fmt.Errorf("audit rotate: %w", err)
		}

		var size int64
		if info, err := os.Stat(rotated); err == nil {
			size = info.Size()
		}

		if err := audit.Record(ctx, domain.NewAuditEvent(
			"wad",
			domain.AuditReload,
			domain.JID{},
			"granted",
			fmt.Sprintf("audit rotated -> %s (%d bytes)", rotated, size),
		)); err != nil {
			log.Warn("audit rotate self-record failed", "err", err)
		}
		log.Info("audit log rotated", "rotated", rotated, "bytes", size)

		return json.Marshal(auditRotateResult{
			Schema:       "wa.admin.audit.rotate/v1",
			RotatedPath:  rotated,
			RotatedBytes: size,
			NewPath:      audit.Path(),
		})
	}
}
