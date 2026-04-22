package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// TestLogoutAllUnsupportedSurfaced verifies that LogoutAll returns
// domain.ErrUpstreamError at the current whatsmeow commit pin (no upstream
// helper), and that no AuditLogoutAll entry is written. Required by T2-12.
func TestLogoutAllUnsupportedSurfaced(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })

	lo, err := a.NewLogoutAllFor()
	if err != nil {
		t.Fatalf("NewLogoutAllFor: %v", err)
	}

	err = lo.LogoutAll(context.Background())
	if err == nil {
		t.Fatalf("LogoutAll = nil, want domain.ErrUpstreamError")
	}
	if !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("LogoutAll err = %v, want wraps domain.ErrUpstreamError", err)
	}

	for _, e := range a.auditBuf.Snapshot() {
		if e.Action == domain.AuditLogoutAll {
			t.Fatalf("failed LogoutAll wrote audit entry: %+v", e)
		}
	}
}

// TestLogoutAllCtxCancelled verifies the standard ctx.Err() guard fires
// before the upstream-unsupported path.
func TestLogoutAllCtxCancelled(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })

	lo, err := a.NewLogoutAllFor()
	if err != nil {
		t.Fatalf("NewLogoutAllFor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lo.LogoutAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("LogoutAll on cancelled ctx err = %v, want context.Canceled", err)
	}
}
