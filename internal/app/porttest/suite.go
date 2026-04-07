// Package porttest is the shared contract test suite for the seven port
// interfaces declared in internal/app. Any secondary adapter can run the
// suite against itself by calling RunContractSuite from a _test.go file
// in its own package. The suite MUST NOT import any concrete adapter
// package; adapters provide themselves via the factory parameter.
package porttest

import (
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// Adapter is the intersection of the seven port interfaces. An adapter
// under test implements this once and feeds instances to the suite via
// the factory parameter of RunContractSuite.
//
// In addition to the seven port methods, the adapter exposes a small
// test-only surface that the suite uses to pre-seed state and to push
// events through the stream without reaching into internals.
type Adapter interface {
	app.MessageSender
	app.EventStream
	app.ContactDirectory
	app.GroupManager
	app.SessionStore
	app.Allowlist
	app.AuditLog

	// SeedContact inserts a contact into the directory. Deterministic
	// tests use this instead of a live network call.
	SeedContact(c domain.Contact)
	// SeedGroup inserts a group into the directory.
	SeedGroup(g domain.Group)
	// EnqueueEvent pushes an event onto the inbound stream for Next().
	EnqueueEvent(e domain.Event)
}

// Factory returns a fresh Adapter for one sub-test. The suite calls it
// once per sub-test so each clause gets a clean state.
type Factory func(t *testing.T) Adapter

// RunContractSuite runs every contract clause against the adapter
// produced by factory. It uses t.Errorf (not t.Fatalf) so a single run
// reports every violation rather than stopping at the first.
func RunContractSuite(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("MessageSender", func(t *testing.T) { testMessageSender(t, factory) })
	t.Run("EventStream", func(t *testing.T) { testEventStream(t, factory) })
	t.Run("ContactDirectory", func(t *testing.T) { testContactDirectory(t, factory) })
	t.Run("GroupManager", func(t *testing.T) { testGroupManager(t, factory) })
	t.Run("SessionStore", func(t *testing.T) { testSessionStore(t, factory) })
	t.Run("AllowlistPort", func(t *testing.T) { testAllowlistPort(t, factory) })
	t.Run("AuditLog", func(t *testing.T) { testAuditLog(t, factory) })
}

// reportf is the canonical failure-mode report format from
// contracts/ports.md §"Failure-mode reporting".
func reportf(t *testing.T, port, method, clause, expected, observed string) {
	t.Helper()
	t.Errorf("[%s.%s/%s] expected %s; got %s", port, method, clause, expected, observed)
}
