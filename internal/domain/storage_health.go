package domain

import "time"

// IngestReceipt carries identity and local receipt time, never message content.
type IngestReceipt struct {
	ChatJID, MessageID string
	ReceivedAt         time.Time
}

// StorageHealth is a cached snapshot; reading it never touches SQLite or the filesystem.
type StorageHealth struct {
	Storage                 string `json:"storage,omitempty"`
	StorageDetail           string `json:"storage_detail,omitempty"`
	LastPersistFailureAt    string `json:"last_persist_failure_at,omitempty"`
	LastPersistFailureUnixS int64  `json:"last_persist_failure_unix_s,omitempty"`
	PersistFailuresTotal    uint64 `json:"persist_failures_total,omitempty"`
	PersistLogicErrorsTotal uint64 `json:"persist_logic_errors_total,omitempty"`
	FreeBytes               uint64 `json:"free_bytes,omitempty"`
	LastReceivedAt          string `json:"last_received_at,omitempty"`
	LastReceivedUnixS       int64  `json:"last_received_unix_s,omitempty"`
	LastPersistedAt         string `json:"last_persisted_at,omitempty"`
	LastPersistedUnixS      int64  `json:"last_persisted_unix_s,omitempty"`
	IngestLagS              int64  `json:"ingest_lag_s,omitempty"`
}
