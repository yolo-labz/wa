package whatsmeow

import (
	"context"
	"testing"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestPrivacyAdapter_SatisfiesContract drives the PrivacySettings contract
// (PV1..PV5) against the whatsmeow adapter with a fake client.
func TestPrivacyAdapter_SatisfiesContract(t *testing.T) {
	porttest.RunPrivacySettingsContract(t, func(t *testing.T) app.PrivacySettings {
		fc := newFakeClient()
		fc.ConnectedFlag = true
		a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
		t.Cleanup(func() { _ = a.Close() })
		p, err := a.NewPrivacyFor()
		if err != nil {
			t.Fatalf("NewPrivacyFor: %v", err)
		}
		return p
	})
}

func newTestPrivacy(t *testing.T) (*PrivacyAdapter, *fakeWhatsmeowClient, *Adapter) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := openWithClient(fc, domain.NewAllowlist(), discardLogger(), time.Now)
	t.Cleanup(func() { _ = a.Close() })
	p, err := a.NewPrivacyFor()
	if err != nil {
		t.Fatalf("NewPrivacyFor: %v", err)
	}
	return p, fc, a
}

// TestPrivacyTupleValidated verifies that Set rejects tuples that fail
// domain.PrivacyTuple.Validate before reaching the wire. Required by T2-11.
func TestPrivacyTupleValidated(t *testing.T) {
	p, fc, _ := newTestPrivacy(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		tuple domain.PrivacyTuple
	}{
		{"zero_key", domain.PrivacyTuple{Key: 0, Value: domain.PrivacyValueEveryone}},
		{"zero_value", domain.PrivacyTuple{Key: domain.PrivacyKeyGroups, Value: 0}},
		{"key_out_of_range", domain.PrivacyTuple{Key: 99, Value: domain.PrivacyValueEveryone}},
		{"value_out_of_range", domain.PrivacyTuple{Key: domain.PrivacyKeyGroups, Value: 99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc.mu.Lock()
			before := len(fc.PrivacyUpdates)
			fc.mu.Unlock()
			if err := p.Set(ctx, tc.tuple); err == nil {
				t.Fatalf("Set(%+v) = nil, want error", tc.tuple)
			}
			fc.mu.Lock()
			after := len(fc.PrivacyUpdates)
			fc.mu.Unlock()
			if after != before {
				t.Fatalf("invalid tuple reached the wire: before=%d after=%d", before, after)
			}
		})
	}
}

// TestPrivacyGetFromServer verifies that every Get forces a server read via
// ignoreCache=true — FR-030 "no local cache" rule — so out-of-band changes
// are reflected immediately. Required by T2-11.
func TestPrivacyGetFromServer(t *testing.T) {
	p, fc, _ := newTestPrivacy(t)
	ctx := context.Background()

	fc.mu.Lock()
	fc.PrivacyCurrent = waTypes.PrivacySettings{
		GroupAdd:     waTypes.PrivacySettingAll,
		ReadReceipts: waTypes.PrivacySettingAll,
		LastSeen:     waTypes.PrivacySettingContacts,
		Profile:      waTypes.PrivacySettingContacts,
		Status:       waTypes.PrivacySettingNone,
	}
	fc.mu.Unlock()

	first, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("Get #1 len = %d, want 5: %+v", len(first), first)
	}
	if fc.PrivacyFetchCalls != 1 {
		t.Fatalf("after Get #1 PrivacyFetchCalls = %d, want 1", fc.PrivacyFetchCalls)
	}

	// Simulate out-of-band change on the server.
	fc.mu.Lock()
	fc.PrivacyCurrent.LastSeen = waTypes.PrivacySettingNone
	fc.mu.Unlock()

	second, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if fc.PrivacyFetchCalls != 2 {
		t.Fatalf("after Get #2 PrivacyFetchCalls = %d, want 2 (no caching allowed)", fc.PrivacyFetchCalls)
	}

	var sawLastSeenNobody bool
	for _, tup := range second {
		if tup.Key == domain.PrivacyKeyLastSeen && tup.Value == domain.PrivacyValueNobody {
			sawLastSeenNobody = true
		}
	}
	if !sawLastSeenNobody {
		t.Fatalf("out-of-band change not reflected: %+v", second)
	}
}

// TestPrivacyAuditWritten verifies that every successful Set writes an
// AuditPrivacyChange entry with the tuple in Detail and "ok" decision.
// Required by T2-11.
func TestPrivacyAuditWritten(t *testing.T) {
	p, _, a := newTestPrivacy(t)
	ctx := context.Background()

	tup := domain.PrivacyTuple{Key: domain.PrivacyKeyReadReceipts, Value: domain.PrivacyValueEveryone}
	if err := p.Set(ctx, tup); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap := a.auditBuf.Snapshot()
	var found bool
	for _, e := range snap {
		if e.Action != domain.AuditPrivacyChange {
			continue
		}
		if e.Decision != "ok" {
			continue
		}
		if e.Detail != "readReceipts=everyone" {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("missing AuditPrivacyChange entry; snap=%+v", snap)
	}
}

// TestPrivacySetHitsWire verifies that Set translates domain enums to the
// matching whatsmeow (name, value) pair and records the call.
func TestPrivacySetHitsWire(t *testing.T) {
	p, fc, _ := newTestPrivacy(t)
	ctx := context.Background()

	cases := []struct {
		tuple     domain.PrivacyTuple
		wantName  waTypes.PrivacySettingType
		wantValue waTypes.PrivacySetting
	}{
		{
			domain.PrivacyTuple{Key: domain.PrivacyKeyGroups, Value: domain.PrivacyValueContacts},
			waTypes.PrivacySettingTypeGroupAdd, waTypes.PrivacySettingContacts,
		},
		{
			domain.PrivacyTuple{Key: domain.PrivacyKeyAbout, Value: domain.PrivacyValueNobody},
			waTypes.PrivacySettingTypeStatus, waTypes.PrivacySettingNone,
		},
		{
			domain.PrivacyTuple{Key: domain.PrivacyKeyLastSeen, Value: domain.PrivacyValueEveryone},
			waTypes.PrivacySettingTypeLastSeen, waTypes.PrivacySettingAll,
		},
	}
	for _, tc := range cases {
		if err := p.Set(ctx, tc.tuple); err != nil {
			t.Fatalf("Set(%+v): %v", tc.tuple, err)
		}
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.PrivacyUpdates) != len(cases) {
		t.Fatalf("PrivacyUpdates len = %d, want %d", len(fc.PrivacyUpdates), len(cases))
	}
	for i, tc := range cases {
		got := fc.PrivacyUpdates[i]
		if got.Name != tc.wantName {
			t.Errorf("call[%d] name = %q, want %q", i, got.Name, tc.wantName)
		}
		if got.Value != tc.wantValue {
			t.Errorf("call[%d] value = %q, want %q", i, got.Value, tc.wantValue)
		}
	}
}
