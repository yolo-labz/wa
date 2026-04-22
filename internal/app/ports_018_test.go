package app_test

// This test file is a pure compile-time gate for feature 018 Tier 2
// port surfaces. If any of the seven new ports (MessageModerator,
// ChatStateManager, Blocker, PrivacySettings, ProfileEditor, GroupAdmin,
// PollManager) or the three MessageSender extension interfaces
// (ForwardSender, StarSender, DisappearingSetter) change in a way that
// breaks the declared method sets, this file stops compiling — which is
// the exact signal T2-01's exit gate specifies.
//
// No runtime behaviour is asserted here; the contract runners under
// internal/app/porttest/ (added by T2-03) exercise semantics.

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// nopMessageModerator implements app.MessageModerator with no-op methods.
type nopMessageModerator struct{}

func (nopMessageModerator) Revoke(context.Context, domain.JID, domain.MessageID, domain.RevokeScope) error {
	return nil
}

func (nopMessageModerator) Edit(context.Context, domain.JID, domain.MessageID, string) error {
	return nil
}

// nopChatStateManager implements app.ChatStateManager with no-op methods.
type nopChatStateManager struct{}

func (nopChatStateManager) Archive(context.Context, domain.JID, bool) error    { return nil }
func (nopChatStateManager) Mute(context.Context, domain.JID, *time.Time) error { return nil }
func (nopChatStateManager) Pin(context.Context, domain.JID, bool) error        { return nil }
func (nopChatStateManager) MarkUnread(context.Context, domain.JID) error       { return nil }

// nopBlocker implements app.Blocker with no-op methods.
type nopBlocker struct{}

func (nopBlocker) Block(context.Context, domain.JID) error           { return nil }
func (nopBlocker) Unblock(context.Context, domain.JID) error         { return nil }
func (nopBlocker) ListBlocked(context.Context) ([]domain.JID, error) { return nil, nil }

// nopPrivacySettings implements app.PrivacySettings with no-op methods.
type nopPrivacySettings struct{}

func (nopPrivacySettings) Set(context.Context, domain.PrivacyTuple) error { return nil }
func (nopPrivacySettings) Get(context.Context) ([]domain.PrivacyTuple, error) {
	return nil, nil
}

// nopProfileEditor implements app.ProfileEditor with no-op methods.
type nopProfileEditor struct{}

func (nopProfileEditor) SetName(context.Context, string) error   { return nil }
func (nopProfileEditor) SetStatus(context.Context, string) error { return nil }
func (nopProfileEditor) GetAvatar(context.Context, domain.JID, domain.AvatarSize) (string, error) {
	return "", nil
}

// nopGroupAdmin implements app.GroupAdmin with no-op methods.
type nopGroupAdmin struct{}

func (nopGroupAdmin) Create(context.Context, string, []domain.JID) (domain.Group, error) {
	return domain.Group{}, nil
}
func (nopGroupAdmin) Leave(context.Context, domain.JID) error { return nil }
func (nopGroupAdmin) AddParticipants(context.Context, domain.JID, []domain.JID) error {
	return nil
}

func (nopGroupAdmin) RemoveParticipants(context.Context, domain.JID, []domain.JID) error {
	return nil
}
func (nopGroupAdmin) Promote(context.Context, domain.JID, []domain.JID) error { return nil }
func (nopGroupAdmin) Demote(context.Context, domain.JID, []domain.JID) error  { return nil }
func (nopGroupAdmin) Edit(context.Context, domain.JID, domain.GroupPatch) error {
	return nil
}

func (nopGroupAdmin) InviteGet(context.Context, domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, nil
}

func (nopGroupAdmin) InviteRevoke(context.Context, domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, nil
}

func (nopGroupAdmin) InviteJoin(context.Context, string) (domain.Group, error) {
	return domain.Group{}, nil
}

// nopPollManager implements app.PollManager with a no-op Vote.
type nopPollManager struct{}

func (nopPollManager) Vote(context.Context, domain.JID, domain.MessageID, []int) error {
	return nil
}

// nopForwardSender / nopStarSender / nopDisappearingSetter exercise the
// three MessageSender extension interfaces.
type nopForwardSender struct{}

func (nopForwardSender) SendForward(context.Context, domain.JID, domain.JID, domain.MessageID) (domain.MessageID, error) {
	return "", nil
}

type nopStarSender struct{}

func (nopStarSender) Star(context.Context, domain.JID, domain.MessageID, bool) error {
	return nil
}

type nopDisappearingSetter struct{}

func (nopDisappearingSetter) SetDisappearing(context.Context, domain.JID, int) error {
	return nil
}

// TestPortsGo018Compiles is T2-01's exit gate: the file compiles iff the
// declared port surfaces match the nop implementations above.
func TestPortsGo018Compiles(t *testing.T) {
	t.Parallel()
	var (
		_ app.MessageModerator   = nopMessageModerator{}
		_ app.ChatStateManager   = nopChatStateManager{}
		_ app.Blocker            = nopBlocker{}
		_ app.PrivacySettings    = nopPrivacySettings{}
		_ app.ProfileEditor      = nopProfileEditor{}
		_ app.GroupAdmin         = nopGroupAdmin{}
		_ app.PollManager        = nopPollManager{}
		_ app.ForwardSender      = nopForwardSender{}
		_ app.StarSender         = nopStarSender{}
		_ app.DisappearingSetter = nopDisappearingSetter{}
	)
}
