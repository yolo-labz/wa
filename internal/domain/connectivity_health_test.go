package domain

import (
	"testing"
	"time"
)

// TestConnectivityHealthState_StringRoundtrip ensures every declared
// value renders a stable wire string and survives IsValid. Spec 110g
// FR-002. Wire stability matters because operators grep these strings
// in `wa events --json`.
func TestConnectivityHealthState_StringRoundtrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state ConnectivityHealthState
		want  string
	}{
		{HealthKeepAliveTimeout, "keepaliveTimeout"},
		{HealthKeepAliveRestored, "keepaliveRestored"},
		{HealthStreamReplaced, "streamReplaced"},
		{HealthConnectFailure, "connectFailure"},
		{HealthTemporaryBan, "temporaryBan"},
		{HealthClientOutdated, "clientOutdated"},
		{HealthManualLoginReconnect, "manualLoginReconnect"},
		{HealthSoftStale, "softStale"},
		{HealthRestored, "restored"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", tc.state, got, tc.want)
		}
		if !tc.state.IsValid() {
			t.Errorf("%d not IsValid", tc.state)
		}
	}
}

// TestConnectivityHealthState_ZeroInvalid asserts the zero value is
// rejected — guards against accidental "" detail emission on a freshly
// allocated struct.
func TestConnectivityHealthState_ZeroInvalid(t *testing.T) {
	t.Parallel()
	var zero ConnectivityHealthState
	if zero.IsValid() {
		t.Error("zero ConnectivityHealthState must be invalid")
	}
	if zero.String() != "unknown" {
		t.Errorf("zero.String() = %q, want %q", zero.String(), "unknown")
	}
}

// TestConnectivityHealthState_OutOfRange covers values above the
// declared enum tail. Defensive: a future PR adding a new variant must
// extend String() / IsValid() in lockstep.
func TestConnectivityHealthState_OutOfRange(t *testing.T) {
	t.Parallel()
	beyond := HealthRestored + 1
	if beyond.IsValid() {
		t.Errorf("%d should not be IsValid", beyond)
	}
	if beyond.String() != "unknown" {
		t.Errorf("%d.String() = %q, want %q", beyond, beyond.String(), "unknown")
	}
}

// TestConnectivityHealthEvent_Interface confirms the struct satisfies
// the sealed Event interface and surfaces ID + TS via the accessors
// that ring-buffer subscribers depend on.
func TestConnectivityHealthEvent_Interface(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	var ev Event = ConnectivityHealthEvent{
		ID:     "soft-1",
		TS:     now,
		State:  HealthSoftStale,
		Detail: "staleSeconds=300 thresholdSec=300",
	}
	if ev.EventID() != "soft-1" {
		t.Errorf("EventID = %q, want %q", ev.EventID(), "soft-1")
	}
	if !ev.Timestamp().Equal(now) {
		t.Errorf("Timestamp = %v, want %v", ev.Timestamp(), now)
	}
}
