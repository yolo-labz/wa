package main

import (
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// timestampAt is the RFC 3339 UTC half of FR-116-17's time pair for a
// unix-seconds value. A missing timestamp (<= 0) yields "" so the field is
// omitted rather than dating the row 1970 — the F7 symptom itself.
func timestampAt(unixS int64) string {
	if unixS <= 0 {
		return ""
	}
	return time.Unix(unixS, 0).UTC().Format(time.RFC3339)
}

// captionEnvelope is an inbound caption alone in its own FR-005a envelope,
// or "" when the row has no caption. The text is escaped, never unwrapped.
func captionEnvelope(m sqlitehistory.StoredMessage) string {
	chat, _ := domain.Parse(m.ChatJID)
	sender, _ := domain.Parse(m.SenderJID)
	return app.ChannelWrapFieldsIf(m.Caption, app.InboundFields{Caption: m.Caption}, chat, sender, m.Timestamp)
}
