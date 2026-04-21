package app

import "testing"

func TestFeatureFlagsDefaults(t *testing.T) {
	got := DefaultFeatureFlags()
	want := FeatureFlags{
		Embeddings:     false,
		ScheduledSends: true,
		Labels:         false,
	}
	if got != want {
		t.Fatalf("DefaultFeatureFlags() = %+v, want %+v", got, want)
	}
}
