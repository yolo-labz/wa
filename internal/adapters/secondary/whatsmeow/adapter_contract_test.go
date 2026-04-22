package whatsmeow

import (
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app/porttest"
	"github.com/yolo-labz/wa/internal/domain"
)

// Compile-time assertion: the whatsmeow adapter satisfies the full
// porttest.Adapter interface. Lives outside the //go:build integration
// scaffold so a missing port method breaks the default build — feature
// 018 R-10 requirement FR-010. If a future port addition trips this,
// extend Adapter rather than hiding behind the integration tag.
var _ porttest.Adapter = (*Adapter)(nil)

// TestContractSuite runs porttest.RunContractSuite against the whatsmeow
// adapter wired to the in-process fakeWhatsmeowClient. The fake lives in
// whatsmeow_client_fake_test.go (no build tag) so the suite stays
// reachable on plain `go test ./...` — no WA_INTEGRATION env var, no
// websocket. Tests requiring a real burner number live in
// adapter_integration_test.go behind //go:build integration.
func TestContractSuite(t *testing.T) {
	porttest.RunContractSuite(t, func(t *testing.T) porttest.Adapter {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		return openWithClient(fc, domain.NewAllowlist(), discardLogger(), func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		})
	})
}
