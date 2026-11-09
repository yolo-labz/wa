package domain

import "time"

// RecoveryEpisode is FR-116-24 evidence for one chat: a pending gap and, once a
// live message has committed after it, the real anchor a deferred pull may use.
// It records evidence; it never claims the missing messages were restored.
type RecoveryEpisode struct {
	ChatJID      string    `json:"chat"`
	Generation   uint64    `json:"generation"`
	Pending      bool      `json:"pending"`
	Unknown      bool      `json:"unknown,omitempty"`
	OutageAt     time.Time `json:"outage_at"`
	AnchorID     string    `json:"anchor_id,omitempty"`
	AnchorTS     int64     `json:"anchor_ts,omitempty"`
	AnchorFromMe bool      `json:"anchor_from_me,omitempty"`
	Missing      int       `json:"unresolved_receipts"`
}
