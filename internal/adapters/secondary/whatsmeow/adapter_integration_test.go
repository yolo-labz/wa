//go:build integration

package whatsmeow

import (
	"os"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app/porttest"
	"github.com/yolo-labz/wa/internal/domain"
)

// This file is the //go:build integration scaffold invoked only when
// WA_INTEGRATION=1 is set in the environment AND the `integration` build
// tag is active. Per feature 003 §"v0 testing strategy" this file is
// NEVER run in CI. The maintainer fills in the TODOs on first run
// against a burner number.

func requireIntegrationEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("WA_INTEGRATION") != "1" {
		t.Skip("WA_INTEGRATION=1 not set; skipping whatsmeow integration test")
	}
}

// TestContractSuite runs porttest.RunContractSuite against the whatsmeow
// adapter wired to an in-process fake client. This exercises the exact
// same clauses the in-memory adapter passes (HS2 skipped per
// SupportsRemoteBackfill gate in the suite unless remote plumbing is
// stubbed out by the maintainer).
func TestContractSuite(t *testing.T) {
	requireIntegrationEnv(t)
	porttest.RunContractSuite(t, func(t *testing.T) porttest.Adapter {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		return openWithClient(fc, domain.NewAllowlist(), discardLogger(), func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		})
	})
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

// Compile-time assertion: the whatsmeow adapter satisfies the full
// porttest.Adapter interface. If a future port addition breaks this
// assertion, add the method on the Adapter rather than hiding behind
// a build tag.
var _ porttest.Adapter = (*Adapter)(nil)
