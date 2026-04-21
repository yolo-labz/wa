package app

// FeatureFlags gates the Tier-3 advanced surface (FR-100/114/122).
//
// Defaults reflect the v0.5 release policy: scheduled sends are the only
// Tier-3 surface that ships on by default (FR-114); embeddings and labels
// require an explicit opt-in.
type FeatureFlags struct {
	Embeddings     bool
	ScheduledSends bool
	Labels         bool
}

// DefaultFeatureFlags returns the zero-config defaults documented in FR-100
// (embeddings off), FR-114 (scheduled sends on), FR-122 (labels off).
func DefaultFeatureFlags() FeatureFlags {
	return FeatureFlags{
		Embeddings:     false,
		ScheduledSends: true,
		Labels:         false,
	}
}
