// Package domain contains the pure-Go entities and invariants of the wa
// project. Files under this package MUST NOT import "go.mau.fi/whatsmeow"
// or any non-stdlib package: this is enforced mechanically by the
// "core-no-whatsmeow" depguard rule in .golangci.yml.
package domain

import "errors"

// Sentinel errors. Every error returned from internal/domain MUST wrap one
// of these via fmt.Errorf("%w: ...", ErrXxx) so callers can errors.Is for
// the category.
var (
	// ErrInvalidJID indicates a malformed or zero JID.
	ErrInvalidJID = errors.New("domain: invalid JID")
	// ErrInvalidPhone indicates a phone number outside ITU-T E.164 [8,15].
	ErrInvalidPhone = errors.New("domain: invalid phone number")
	// ErrEmptyBody indicates a required body or path was empty.
	ErrEmptyBody = errors.New("domain: message body must not be empty")
	// ErrMessageTooLarge indicates a message exceeds its variant size limit.
	ErrMessageTooLarge = errors.New("domain: message exceeds size limit")
	// ErrUnknownAction indicates ParseAction received an unknown string.
	ErrUnknownAction = errors.New("domain: unknown action")
	// ErrNotAllowed is reserved for the app-layer policy middleware.
	ErrNotAllowed = errors.New("domain: action not allowed for jid")
)
