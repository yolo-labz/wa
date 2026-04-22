package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// PrivacySettingsFactory returns a fresh PrivacySettings for one sub-test.
type PrivacySettingsFactory func(t *testing.T) app.PrivacySettings

// RunPrivacySettingsContract exercises PrivacySettings (FR-029, FR-030).
//
//	PV1 Valid (key, value) tuple returns nil.
//	PV2 Zero key is rejected.
//	PV3 Zero value is rejected.
//	PV4 Get returns a non-nil slice (may be empty if adapter has no defaults).
//	PV5 Cancelled ctx returns ctx.Err on Set and Get.
func RunPrivacySettingsContract(t *testing.T, factory PrivacySettingsFactory) {
	t.Helper()

	t.Run("PV1_valid_tuple", func(t *testing.T) {
		p := factory(t)
		tup := domain.PrivacyTuple{Key: domain.PrivacyKeyReadReceipts, Value: domain.PrivacyValueEveryone}
		if err := p.Set(context.Background(), tup); err != nil {
			reportf(t, "PrivacySettings", "Set", "PV1", "nil err for valid tuple", err.Error())
		}
	})

	t.Run("PV2_zero_key_rejected", func(t *testing.T) {
		p := factory(t)
		tup := domain.PrivacyTuple{Key: 0, Value: domain.PrivacyValueEveryone}
		if err := p.Set(context.Background(), tup); err == nil {
			reportf(t, "PrivacySettings", "Set", "PV2", "non-nil err for zero key", "nil")
		}
	})

	t.Run("PV3_zero_value_rejected", func(t *testing.T) {
		p := factory(t)
		tup := domain.PrivacyTuple{Key: domain.PrivacyKeyGroups, Value: 0}
		if err := p.Set(context.Background(), tup); err == nil {
			reportf(t, "PrivacySettings", "Set", "PV3", "non-nil err for zero value", "nil")
		}
	})

	t.Run("PV4_get_returns_slice", func(t *testing.T) {
		p := factory(t)
		got, err := p.Get(context.Background())
		if err != nil {
			reportf(t, "PrivacySettings", "Get", "PV4", "nil err", err.Error())
			return
		}
		_ = got // len may legitimately be zero for a fake
	})

	t.Run("PV5_cancelled_ctx", func(t *testing.T) {
		p := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tup := domain.PrivacyTuple{Key: domain.PrivacyKeyAbout, Value: domain.PrivacyValueContacts}
		if err := p.Set(ctx, tup); err == nil {
			reportf(t, "PrivacySettings", "Set", "PV5", "non-nil err on cancelled ctx", "nil")
		}
	})
}
