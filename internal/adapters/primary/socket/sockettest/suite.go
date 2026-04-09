package sockettest

import "testing"

// RunSuite is the contract test suite entry point. It accepts a factory that
// builds a server from a Dispatcher so the suite can be reused against any
// future primary adapter implementation.
//
// Subtests will be added as US1-US5 are implemented. For now this is a
// placeholder that verifies the scaffolding compiles and runs.
func RunSuite(t *testing.T, newServer func(d *FakeDispatcher) /* *Server — typed in US1 */) {
	t.Helper()
	t.Log("suite placeholder — subtests will be added with US1-US5 implementation")
}
