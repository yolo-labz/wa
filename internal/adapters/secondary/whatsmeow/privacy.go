package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

// PrivacyAdapter is the whatsmeow-backed implementation of
// app.PrivacySettings (FR-029, FR-030). Set sends a single privacy IQ
// through Client.SetPrivacySetting and writes AuditPrivacyChange on
// success; Get forces a server read (ignoreCache=true) so changes made
// from other devices are reflected immediately, per the "no local
// cache" rule in ports_018.go.
//
// Value-space mapping (domain → whatsmeow):
//
//	everyone -> PrivacySettingAll
//	contacts -> PrivacySettingContacts
//	nobody   -> PrivacySettingNone
//
// Key-space mapping (domain → whatsmeow):
//
//	groups       -> groupadd
//	readReceipts -> readreceipts
//	lastSeen     -> last
//	profile      -> profile
//	about        -> status  (WA's "status" IQ attribute is the "About" text;
//	                         the separate "stories" visibility is not exposed
//	                         through our port).
type PrivacyAdapter struct {
	client whatsmeowClient
	audit  *auditRingBuffer
	logger *slog.Logger
	nowFn  func() time.Time
}

// NewPrivacyFor wires a PrivacyAdapter to the Adapter's whatsmeow
// client, audit ring, logger, and clock.
func (a *Adapter) NewPrivacyFor() (*PrivacyAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewPrivacyFor: nil adapter/client")
	}
	return &PrivacyAdapter{
		client: a.client,
		audit:  a.auditBuf,
		logger: a.logger,
		nowFn:  a.nowFn,
	}, nil
}

// Set implements app.PrivacySettings. Validates the tuple against the
// domain enum, translates to whatsmeow terms, fires the IQ, and writes
// an AuditPrivacyChange entry on success.
func (p *PrivacyAdapter) Set(ctx context.Context, tuple domain.PrivacyTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tuple.Validate(); err != nil {
		return fmt.Errorf("PrivacyAdapter.Set: %w", err)
	}
	if !p.client.IsConnected() {
		return fmt.Errorf("PrivacyAdapter.Set: %w", domain.ErrDisconnected)
	}
	name, err := privacyKeyToWhatsmeow(tuple.Key)
	if err != nil {
		return fmt.Errorf("PrivacyAdapter.Set: %w", err)
	}
	value, err := privacyValueToWhatsmeow(tuple.Value)
	if err != nil {
		return fmt.Errorf("PrivacyAdapter.Set: %w", err)
	}
	if _, err := p.client.SetPrivacySetting(ctx, name, value); err != nil {
		return fmt.Errorf("PrivacyAdapter.Set: %w", err)
	}
	p.recordAudit(tuple, "ok", "")
	return nil
}

// Get implements app.PrivacySettings. Forces a server read via
// ignoreCache=true — the contract demands out-of-band changes (phone
// UI, other paired devices) be visible immediately. Returns only the
// five keys the domain declares; any unknown or undefined values are
// skipped so partially-hydrated servers don't leak into the port's
// contract.
func (p *PrivacyAdapter) Get(ctx context.Context) ([]domain.PrivacyTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.client.IsConnected() {
		return nil, fmt.Errorf("PrivacyAdapter.Get: %w", domain.ErrDisconnected)
	}
	settings, err := p.client.TryFetchPrivacySettings(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("PrivacyAdapter.Get: %w", err)
	}
	if settings == nil {
		return nil, nil
	}
	pairs := []struct {
		Key   domain.PrivacyKey
		Value waTypes.PrivacySetting
	}{
		{domain.PrivacyKeyGroups, settings.GroupAdd},
		{domain.PrivacyKeyReadReceipts, settings.ReadReceipts},
		{domain.PrivacyKeyLastSeen, settings.LastSeen},
		{domain.PrivacyKeyProfile, settings.Profile},
		{domain.PrivacyKeyAbout, settings.Status},
	}
	out := make([]domain.PrivacyTuple, 0, len(pairs))
	for _, pair := range pairs {
		dv, ok := privacyValueFromWhatsmeow(pair.Value)
		if !ok {
			if p.logger != nil {
				p.logger.Warn("PrivacyAdapter.Get: skipping unmapped value",
					"key", pair.Key.String(), "raw", string(pair.Value))
			}
			continue
		}
		out = append(out, domain.PrivacyTuple{Key: pair.Key, Value: dv})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out, nil
}

func (p *PrivacyAdapter) recordAudit(tuple domain.PrivacyTuple, decision, detail string) {
	if p.audit == nil {
		return
	}
	_ = p.audit.Record(context.Background(), domain.AuditEvent{
		TS:       p.nowFn(),
		Actor:    "whatsmeow",
		Action:   domain.AuditPrivacyChange,
		Subject:  domain.JID{},
		Decision: decision,
		Detail:   tuple.Key.String() + "=" + tuple.Value.String() + detailSeparator(detail) + detail,
	})
}

func detailSeparator(detail string) string {
	if detail == "" {
		return ""
	}
	return "; "
}

func privacyKeyToWhatsmeow(k domain.PrivacyKey) (waTypes.PrivacySettingType, error) {
	switch k {
	case domain.PrivacyKeyGroups:
		return waTypes.PrivacySettingTypeGroupAdd, nil
	case domain.PrivacyKeyReadReceipts:
		return waTypes.PrivacySettingTypeReadReceipts, nil
	case domain.PrivacyKeyLastSeen:
		return waTypes.PrivacySettingTypeLastSeen, nil
	case domain.PrivacyKeyProfile:
		return waTypes.PrivacySettingTypeProfile, nil
	case domain.PrivacyKeyAbout:
		return waTypes.PrivacySettingTypeStatus, nil
	default:
		return "", fmt.Errorf("unsupported privacy key %d", uint8(k))
	}
}

func privacyValueToWhatsmeow(v domain.PrivacyValue) (waTypes.PrivacySetting, error) {
	switch v {
	case domain.PrivacyValueEveryone:
		return waTypes.PrivacySettingAll, nil
	case domain.PrivacyValueContacts:
		return waTypes.PrivacySettingContacts, nil
	case domain.PrivacyValueNobody:
		return waTypes.PrivacySettingNone, nil
	default:
		return "", fmt.Errorf("unsupported privacy value %d", uint8(v))
	}
}

func privacyValueFromWhatsmeow(v waTypes.PrivacySetting) (domain.PrivacyValue, bool) {
	switch v {
	case waTypes.PrivacySettingAll:
		return domain.PrivacyValueEveryone, true
	case waTypes.PrivacySettingContacts:
		return domain.PrivacyValueContacts, true
	case waTypes.PrivacySettingNone:
		return domain.PrivacyValueNobody, true
	default:
		return 0, false
	}
}
