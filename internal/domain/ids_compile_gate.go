//go:build never
// +build never

// This file is INTENTIONALLY excluded from the default build (build tag
// "never"). Its purpose is to be compiled on demand via
//     go build -tags never ./internal/domain/
// and to FAIL with four "cannot use X (type Y) as Z" errors, proving the
// named-type property for MessageID and EventID holds.

package domain

func testCrossTypeAssignmentForbidden() { //nolint:unused // intentionally dead code; compile-gate only
	var m MessageID
	var e EventID
	var s string
	_ = m
	_ = e
	_ = s
	m = e // MUST fail: cannot use e (EventID) as MessageID
	e = m // MUST fail: cannot use m (MessageID) as EventID
	m = s // MUST fail: cannot use s (string) as MessageID
	e = s // MUST fail: cannot use s (string) as EventID
}
