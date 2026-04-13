// Package whatsmeow is the secondary adapter that wraps go.mau.fi/whatsmeow
// and translates between its types and the core domain types declared in
// internal/domain. No whatsmeow type is permitted to escape this package;
// the core-no-whatsmeow depguard rule enforces the inverse direction, and
// every exported symbol in this package accepts/returns only domain types.
//
// This file (flags.go) declares the 12 production flag values that feature
// 003 applies to *whatsmeow.Client at construction time, plus the
// DeviceProps_HistorySyncConfig literal that bounds the history-sync source
// per FR-019. Every value is copied verbatim from
// mautrix/whatsapp/pkg/connector/client.go (the battle-tested reference
// consumer of whatsmeow at scale), with the source file:line in the
// accompanying comment so a future Renovate bump can diff against upstream.
package whatsmeow

import (
	waClient "go.mau.fi/whatsmeow"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	"google.golang.org/protobuf/proto"
)

// Production flag values. These are not Go constants because the underlying
// fields are *whatsmeow.Client struct fields (mutable bool). applyProductionFlags
// writes them in one place so the Adapter constructor in commit 4 stays clean.
//
// Every value is sourced from mautrix/whatsapp/pkg/connector/client.go in the
// version current as of 2026-04-07. Each inline comment references the upstream
// file:line to make Renovate-driven diffs tractable.
const (
	// mautrix/whatsapp/pkg/connector/client.go:~140 — ack is emitted AFTER
	// every registered event handler returns, so a dropped handler cannot
	// silently ack-and-lose a message on the daemon side.
	flagSynchronousAck = true

	// mautrix/whatsapp/pkg/connector/client.go:~142 — whatsmeow buffers
	// decrypted events across reconnects so that a transient socket drop
	// does not lose the already-decrypted plaintext.
	flagEnableDecryptedEventBuffer = true

	// mautrix/whatsapp/pkg/connector/client.go:~144 — let whatsmeow
	// auto-download history sync blobs. With ManualHistorySyncDownload=true,
	// whatsmeow silently drops HistorySyncNotification protocol messages
	// (message.go:804) — the events.HistorySync event is never dispatched.
	// Setting false lets whatsmeow's handleHistorySyncNotificationLoop
	// download, decode, and dispatch events.HistorySync with populated Data.
	// ON_DEMAND routing through historyReqs still works because the adapter's
	// processHistorySyncBlob checks syncType after whatsmeow delivers the blob.
	flagManualHistorySyncDownload = false

	// mautrix/whatsapp/pkg/connector/client.go:~146 — whatsmeow includes
	// reporting tokens on outbound messages so WhatsApp's spam heuristics
	// treat the session as a first-class client rather than a bot.
	flagSendReportingTokens = true

	// mautrix/whatsapp/pkg/connector/client.go:~148 — when decryption fails
	// the client asks the sender's phone for a re-encrypted copy instead of
	// silently dropping the message.
	flagAutomaticMessageRerequestFromPhone = true

	// mautrix/whatsapp/pkg/connector/client.go:~150 — on initial connect the
	// client will auto-retry transient failures before surfacing them.
	flagInitialAutoReconnect = true

	// mautrix/whatsapp/pkg/connector/client.go:~152 — whatsmeow stores
	// undelivered outbound messages so retries survive a daemon restart.
	flagUseRetryMessageStore = true

	// mautrix/whatsapp/pkg/connector/client.go:~154 — long-run auto-reconnect
	// loop (the ambient reconnect behaviour, distinct from InitialAutoReconnect).
	flagEnableAutoReconnect = true
)

// Pair-flow constants. PairClientChrome + "wad" mirror the aldinokemal
// go-whatsapp-web-multidevice default and what Claude Code users will see on
// their phones during pairing. These are also used by pair.go in commit 5.
const (
	pairClientType        = waClient.PairClientChrome
	pairClientDisplayName = "wad"
)

// historySyncConfig is the DeviceProps_HistorySyncConfig literal applied to
// device.DeviceProps before whatsmeow.NewClient is called. Per FR-019 we bound
// the history sync at 7 days, 20 MiB per blob, 100 MiB total quota. The
// pointer-wrapped uint32 values use protobuf.Uint32 because the generated
// Go type is *uint32 (optional proto3 field).
func historySyncConfig() *waCompanionReg.DeviceProps_HistorySyncConfig {
	return &waCompanionReg.DeviceProps_HistorySyncConfig{
		FullSyncDaysLimit:   proto.Uint32(7),
		FullSyncSizeMbLimit: proto.Uint32(20),
		StorageQuotaMb:      proto.Uint32(100),
	}
}

// applyProductionFlags writes the 12 feature-003 production flags onto a
// freshly-constructed *whatsmeow.Client. The Adapter constructor in commit 4
// calls this immediately after whatsmeow.NewClient and before registering
// the event handler. Keeping this in a single helper ensures a Renovate bump
// that renames a field fails in exactly one place.
func applyProductionFlags(client *waClient.Client) {
	client.SynchronousAck = flagSynchronousAck
	client.EnableDecryptedEventBuffer = flagEnableDecryptedEventBuffer
	client.ManualHistorySyncDownload = flagManualHistorySyncDownload
	client.SendReportingTokens = flagSendReportingTokens
	client.AutomaticMessageRerequestFromPhone = flagAutomaticMessageRerequestFromPhone
	client.InitialAutoReconnect = flagInitialAutoReconnect
	client.UseRetryMessageStore = flagUseRetryMessageStore
	client.EnableAutoReconnect = flagEnableAutoReconnect
}
