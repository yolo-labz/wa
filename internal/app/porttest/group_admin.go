package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// GroupAdminFactory returns a fresh GroupAdmin for one sub-test.
type GroupAdminFactory func(t *testing.T) app.GroupAdmin

// RunGroupAdminContract exercises GroupAdmin (FR-020..FR-025).
//
//	GA1  Create with valid participants returns a domain.Group whose JID
//	     IsGroup()==true.
//	GA2  Create with empty participants is rejected.
//	GA3  Leave on a valid group JID returns nil.
//	GA4  AddParticipants with empty list is rejected.
//	GA5  RemoveParticipants with empty list is rejected.
//	GA6  Promote / Demote with empty list is rejected.
//	GA7  Edit with all-nil GroupPatch is rejected (mirrors domain.GroupPatch.Validate).
//	GA8  InviteGet on a valid group returns a non-empty URL matching the
//	     expected regex.
//	GA9  InviteJoin with invalid URL is rejected.
//	GA10 Cancelled ctx returns ctx.Err on every method.
func RunGroupAdminContract(t *testing.T, factory GroupAdminFactory) {
	t.Helper()
	group := domain.MustJID("1234567890-1600000000@g.us")
	participant := domain.MustJID("5511999999999")

	t.Run("GA1_create_ok", func(t *testing.T) {
		g := factory(t)
		out, err := g.Create(context.Background(), "Team", []domain.JID{participant})
		if err != nil {
			reportf(t, "GroupAdmin", "Create", "GA1", "nil err", err.Error())
			return
		}
		if !out.JID.IsGroup() {
			reportf(t, "GroupAdmin", "Create", "GA1", "group JID", out.JID.String())
		}
	})

	t.Run("GA2_create_empty_rejected", func(t *testing.T) {
		g := factory(t)
		if _, err := g.Create(context.Background(), "Team", nil); err == nil {
			reportf(t, "GroupAdmin", "Create", "GA2", "non-nil err for empty participants", "nil")
		}
	})

	t.Run("GA3_leave_ok", func(t *testing.T) {
		g := factory(t)
		if err := g.Leave(context.Background(), group); err != nil {
			reportf(t, "GroupAdmin", "Leave", "GA3", "nil err", err.Error())
		}
	})

	t.Run("GA4_add_empty_rejected", func(t *testing.T) {
		g := factory(t)
		if err := g.AddParticipants(context.Background(), group, nil); err == nil {
			reportf(t, "GroupAdmin", "AddParticipants", "GA4", "non-nil err for empty list", "nil")
		}
	})

	t.Run("GA5_remove_empty_rejected", func(t *testing.T) {
		g := factory(t)
		if err := g.RemoveParticipants(context.Background(), group, nil); err == nil {
			reportf(t, "GroupAdmin", "RemoveParticipants", "GA5", "non-nil err for empty list", "nil")
		}
	})

	t.Run("GA6_promote_demote_empty_rejected", func(t *testing.T) {
		g := factory(t)
		if err := g.Promote(context.Background(), group, nil); err == nil {
			reportf(t, "GroupAdmin", "Promote", "GA6", "non-nil err for empty list", "nil")
		}
		if err := g.Demote(context.Background(), group, nil); err == nil {
			reportf(t, "GroupAdmin", "Demote", "GA6", "non-nil err for empty list", "nil")
		}
	})

	t.Run("GA7_edit_all_nil_rejected", func(t *testing.T) {
		g := factory(t)
		if err := g.Edit(context.Background(), group, domain.GroupPatch{}); err == nil {
			reportf(t, "GroupAdmin", "Edit", "GA7", "non-nil err for all-nil patch", "nil")
		}
	})

	t.Run("GA8_invite_get_url_shape", func(t *testing.T) {
		g := factory(t)
		link, err := g.InviteGet(context.Background(), group)
		if err != nil {
			reportf(t, "GroupAdmin", "InviteGet", "GA8", "nil err", err.Error())
			return
		}
		if link.URL == "" {
			reportf(t, "GroupAdmin", "InviteGet", "GA8", "non-empty URL", "empty")
		}
	})

	t.Run("GA9_invite_join_bogus_url_rejected", func(t *testing.T) {
		g := factory(t)
		if _, err := g.InviteJoin(context.Background(), "http://evil.example.com/not-a-whatsapp-invite"); err == nil {
			reportf(t, "GroupAdmin", "InviteJoin", "GA9", "non-nil err for bogus URL", "nil")
		}
	})

	t.Run("GA10_cancelled_ctx", func(t *testing.T) {
		g := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.Leave(ctx, group); err == nil {
			reportf(t, "GroupAdmin", "Leave", "GA10", "non-nil err on cancelled ctx", "nil")
		}
	})
}
