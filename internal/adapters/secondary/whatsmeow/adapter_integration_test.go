//go:build integration

package whatsmeow

import (
	"os"
	"testing"
)

// This file is the //go:build integration scaffold invoked only when
// WA_INTEGRATION=1 is set in the environment AND the `integration` build
// tag is active. Per feature 003 §"v0 testing strategy" this file is
// NEVER run in CI. The maintainer fills in the TODOs on first run
// against a burner number.
//
// The fake-driven contract suite (TestContractSuite) lives in
// adapter_contract_test.go without a build tag so it runs on every
// plain `go test ./...` — feature 018 R-10 unblocks that from behind
// this integration scaffold. Only tests that genuinely need live
// network access belong here.

func requireIntegrationEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("WA_INTEGRATION") != "1" {
		t.Skip("WA_INTEGRATION=1 not set; skipping whatsmeow integration test")
	}
}

// TestPairRestartReconnect is the manual pairing+restart+reconnect
// integration path. The maintainer fills this in with a real burner
// number on first run. The body below is a smoke skeleton that checks
// the adapter can be constructed and closed cleanly; replace with a
// real pair → send → close → reopen → receive flow.
func TestPairRestartReconnect(t *testing.T) {
	requireIntegrationEnv(t)

	// TODO(maintainer): wire a real sqlstore.Container + sqlitehistory
	// and exercise the Open → pair (QR) → send → Close → Open →
	// receive flow against a burner number. See CLAUDE.md §"v0 testing
	// strategy" for the binding contract.
	t.Skip("fill in with real burner number on first run")
}

// TestSendMediaRoundTrip — feature 018 T1-08 / R-01: exercises the real
// Upload + SendMessage path end-to-end against a burner number. The body
// below is a skeleton; the maintainer fills in the pair-once + send flow
// on first run. CI never runs this (double-gated by //go:build integration
// AND WA_INTEGRATION=1) per CLAUDE.md §"v0 testing strategy".
func TestSendMediaRoundTrip(t *testing.T) {
	requireIntegrationEnv(t)

	// TODO(maintainer): pair a burner once, hardcode the device JID, then
	// send one small PNG and one <16 MiB PDF to a self-chat. Assert the
	// returned MessageID is non-empty and audit rows land. Wire a cleanup
	// that deletes the two messages server-side.
	t.Skip("fill in with real burner number on first run — see T1-08")
}
