// Package memory is the in-memory implementation of the seven secondary
// ports declared in internal/app. It is the canonical test fake and the
// seed for the future --dry-run mode.
package memory

import "time"

// Clock is the package-scoped time source. It is intentionally NOT one
// of the seven ports (see data-model.md §"Why Clock is not a port").
// Production wiring uses RealClock; tests use FakeClock to pin time.
type Clock interface {
	Now() time.Time
}

// RealClock delegates to time.Now.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// FakeClock is a deterministic clock for tests. It is NOT safe for
// concurrent mutation from multiple goroutines; tests that advance the
// clock should do so from the main goroutine.
type FakeClock struct {
	T time.Time
}

// Now returns the clock's fixed time.
func (f *FakeClock) Now() time.Time { return f.T }

// Advance moves the fake clock forward by d.
func (f *FakeClock) Advance(d time.Duration) { f.T = f.T.Add(d) }
