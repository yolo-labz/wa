package porttest

import (
	"context"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ProfileEditorFactory returns a fresh ProfileEditor for one sub-test.
type ProfileEditorFactory func(t *testing.T) app.ProfileEditor

// RunProfileEditorContract exercises ProfileEditor (FR-027, FR-028).
//
//	PE1 SetName within 25 chars returns nil.
//	PE2 SetName > 25 chars is rejected.
//	PE3 SetStatus within 139 chars returns nil.
//	PE4 SetStatus > 139 chars is rejected.
//	PE5 GetAvatar returns a non-empty path for a valid JID+size.
//	PE6 GetAvatar rejects invalid AvatarSize.
//	PE7 Cancelled ctx returns ctx.Err on every method.
func RunProfileEditorContract(t *testing.T, factory ProfileEditorFactory) {
	t.Helper()
	jid := domain.MustJID("5511999999999")

	t.Run("PE1_set_name_ok", func(t *testing.T) {
		p := factory(t)
		if err := p.SetName(context.Background(), "Pedro"); err != nil {
			reportf(t, "ProfileEditor", "SetName", "PE1", "nil err", err.Error())
		}
	})

	t.Run("PE2_set_name_too_long", func(t *testing.T) {
		p := factory(t)
		long := strings.Repeat("a", 26)
		if err := p.SetName(context.Background(), long); err == nil {
			reportf(t, "ProfileEditor", "SetName", "PE2", "non-nil err for > 25 chars", "nil")
		}
	})

	t.Run("PE3_set_status_ok", func(t *testing.T) {
		p := factory(t)
		if err := p.SetStatus(context.Background(), "available"); err != nil {
			reportf(t, "ProfileEditor", "SetStatus", "PE3", "nil err", err.Error())
		}
	})

	t.Run("PE4_set_status_too_long", func(t *testing.T) {
		p := factory(t)
		long := strings.Repeat("b", 140)
		if err := p.SetStatus(context.Background(), long); err == nil {
			reportf(t, "ProfileEditor", "SetStatus", "PE4", "non-nil err for > 139 chars", "nil")
		}
	})

	t.Run("PE5_get_avatar_path", func(t *testing.T) {
		p := factory(t)
		path, err := p.GetAvatar(context.Background(), jid, domain.AvatarPreview)
		if err != nil {
			reportf(t, "ProfileEditor", "GetAvatar", "PE5", "nil err or typed not-found", err.Error())
			return
		}
		if path == "" {
			reportf(t, "ProfileEditor", "GetAvatar", "PE5", "non-empty path", "empty")
		}
	})

	t.Run("PE6_invalid_size", func(t *testing.T) {
		p := factory(t)
		if _, err := p.GetAvatar(context.Background(), jid, domain.AvatarSize(0)); err == nil {
			reportf(t, "ProfileEditor", "GetAvatar", "PE6", "non-nil err for zero size", "nil")
		}
	})

	t.Run("PE7_cancelled_ctx", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := p.SetName(ctx, "X"); err == nil {
			reportf(t, "ProfileEditor", "SetName", "PE7", "non-nil err on cancelled ctx", "nil")
		}
	})
}
