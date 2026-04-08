// Package sockettest provides a reusable contract test suite and a
// FakeDispatcher for testing the socket primary adapter.
package sockettest

// Dependency pin: imported in test code from Phase 2 onward.
// This blank import ensures go mod tidy retains the module in go.mod
// until real imports land.
import _ "go.uber.org/goleak"
