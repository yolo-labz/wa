package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	adminGroupJID = "120363000000000001@g.us"
	adminMember   = "5511777777777@s.whatsapp.net"
)

// groupAdminRec records the group-admin calls the dispatcher forwards.
type groupAdminRec struct {
	calls []string
	patch domain.GroupPatch
	err   error
}

func (g *groupAdminRec) note(s string) error { g.calls = append(g.calls, s); return g.err }

func (g *groupAdminRec) Create(_ context.Context, subject string, p []domain.JID) (domain.Group, error) {
	return domain.Group{JID: domain.MustJID(adminGroupJID), Subject: subject, Participants: p}, g.note("create:" + subject)
}
func (g *groupAdminRec) Leave(context.Context, domain.JID) error { return g.note("leave") }
func (g *groupAdminRec) AddParticipants(context.Context, domain.JID, []domain.JID) error {
	return g.note("addParticipants")
}

func (g *groupAdminRec) RemoveParticipants(context.Context, domain.JID, []domain.JID) error {
	return g.note("removeParticipants")
}

func (g *groupAdminRec) Promote(context.Context, domain.JID, []domain.JID) error {
	return g.note("promote")
}

func (g *groupAdminRec) Demote(context.Context, domain.JID, []domain.JID) error {
	return g.note("demote")
}

func (g *groupAdminRec) Edit(_ context.Context, _ domain.JID, p domain.GroupPatch) error {
	g.patch = p
	return g.note("edit")
}

func (g *groupAdminRec) InviteGet(_ context.Context, j domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{Group: j, URL: "https://chat.whatsapp.com/CODE", Code: "CODE"}, g.note("inviteGet")
}

func (g *groupAdminRec) InviteRevoke(_ context.Context, j domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{Group: j, URL: "https://chat.whatsapp.com/NEW", Code: "NEW"}, g.note("inviteRevoke")
}

func (g *groupAdminRec) InviteJoin(context.Context, string) (domain.Group, error) {
	return domain.Group{JID: domain.MustJID(adminGroupJID)}, g.note("inviteJoin")
}

func groupAdminDispatcher(t *testing.T, ga app.GroupAdmin) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: adapter, Events: adapter, Contacts: adapter, Groups: adapter, Session: adapter,
		Allowlist: adapter, Audit: adapter, History: adapter, Pairer: adapter, Quoted: adapter,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
		Websocket:      &fakeProbe{up: true},
		GroupAdmin:     ga,
	})
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

func TestGroupAdmin_ForwardsEachMethod(t *testing.T) {
	t.Parallel()
	roster := map[string]any{"group": adminGroupJID, "participants": []string{adminMember}}
	cases := []struct {
		method string
		params map[string]any
		want   string
		inRes  string
	}{
		{"group.create", map[string]any{"subject": "Team", "participants": []string{adminMember}}, "create:Team", adminGroupJID},
		{"group.leave", map[string]any{"group": adminGroupJID}, "leave", ""},
		{"group.addParticipants", roster, "addParticipants", ""},
		{"group.removeParticipants", roster, "removeParticipants", ""},
		{"group.promote", roster, "promote", ""},
		{"group.demote", roster, "demote", ""},
		{"group.edit", map[string]any{"group": adminGroupJID, "subject": "New", "description": "d"}, "edit", ""},
		{"group.inviteGet", map[string]any{"group": adminGroupJID}, "inviteGet", "CODE"},
		{"group.inviteRevoke", map[string]any{"group": adminGroupJID}, "inviteRevoke", "NEW"},
		{"group.inviteJoin", map[string]any{"url": "https://chat.whatsapp.com/CODE"}, "inviteJoin", adminGroupJID},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			t.Parallel()
			rec := &groupAdminRec{}
			d, adapter := groupAdminDispatcher(t, rec)
			adapter.Grant(domain.MustJID(adminMember), domain.ActionGroupAdd)
			raw, _ := json.Marshal(c.params)
			out, err := d.Handle(context.Background(), c.method, raw)
			if err != nil {
				t.Fatalf("Handle(%s): %v", c.method, err)
			}
			if len(rec.calls) != 1 || rec.calls[0] != c.want {
				t.Errorf("calls = %v, want [%s]", rec.calls, c.want)
			}
			if !strings.Contains(string(out), c.inRes) {
				t.Errorf("result %s lacks %q", out, c.inRes)
			}
			if c.method == "group.edit" && (rec.patch.Subject == nil || *rec.patch.Subject != "New" || rec.patch.IconPath != nil) {
				t.Errorf("patch = %+v, want subject New and no icon", rec.patch)
			}
		})
	}
}

func TestGroupAdmin_RefusesBadInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, params string
		want           error
	}{
		{"group.create", `{"subject":"x"}`, app.ErrInvalidParams},
		{"group.create", `{"subject":"x","participants":["nope"]}`, app.ErrInvalidJID},
		{"group.leave", `{}`, app.ErrInvalidParams},
		{"group.leave", `{"group":"nope"}`, app.ErrInvalidJID},
		{"group.removeParticipants", `{"group":"` + adminGroupJID + `"}`, app.ErrInvalidParams},
		{"group.promote", `{"group":"nope","participants":["` + adminMember + `"]}`, app.ErrInvalidJID},
		{"group.demote", `{"group":"` + adminGroupJID + `","participants":["nope"]}`, app.ErrInvalidJID},
		{"group.edit", `{}`, app.ErrInvalidParams},
		{"group.edit", `{"group":"nope"}`, app.ErrInvalidJID},
		{"group.inviteGet", `{}`, app.ErrInvalidParams},
		{"group.inviteRevoke", `{"group":"nope"}`, app.ErrInvalidJID},
		{"group.inviteJoin", `{}`, app.ErrInvalidParams},
	} {
		t.Run(tc.method+" "+tc.params, func(t *testing.T) {
			t.Parallel()
			rec := &groupAdminRec{}
			d, adapter := groupAdminDispatcher(t, rec)
			adapter.Grant(domain.MustJID(adminMember), domain.ActionGroupAdd)
			if _, err := d.Handle(context.Background(), tc.method, json.RawMessage(tc.params)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(rec.calls) != 0 {
				t.Errorf("a refused call reached the port: %v", rec.calls)
			}
		})
	}
}

// Adding people is default-deny: a participant that is not allowlisted for
// group.add stops both group.create and group.addParticipants before the port.
func TestGroupAdmin_AddingNeedsTheAllowlist(t *testing.T) {
	t.Parallel()
	rec := &groupAdminRec{}
	d, _ := groupAdminDispatcher(t, rec)
	for method, p := range map[string]map[string]any{
		"group.create":          {"subject": "x", "participants": []string{adminMember}},
		"group.addParticipants": {"group": adminGroupJID, "participants": []string{adminMember}},
	} {
		raw, _ := json.Marshal(p)
		if _, err := d.Handle(context.Background(), method, raw); err == nil {
			t.Errorf("%s of a participant outside the allowlist was accepted", method)
		}
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refused call reached the port: %v", rec.calls)
	}
}

func TestGroupAdmin_PortErrorsNameTheMethod(t *testing.T) {
	t.Parallel()
	roster := map[string]any{"group": adminGroupJID, "participants": []string{adminMember}}
	for method, p := range map[string]map[string]any{
		"group.create":             {"subject": "x", "participants": []string{adminMember}},
		"group.leave":              {"group": adminGroupJID},
		"group.removeParticipants": roster,
		"group.edit":               {"group": adminGroupJID, "subject": "x"},
		"group.inviteGet":          {"group": adminGroupJID},
		"group.inviteRevoke":       {"group": adminGroupJID},
		"group.inviteJoin":         {"url": "https://chat.whatsapp.com/CODE"},
	} {
		d, adapter := groupAdminDispatcher(t, &groupAdminRec{err: errors.New("boom")})
		adapter.Grant(domain.MustJID(adminMember), domain.ActionGroupAdd)
		raw, _ := json.Marshal(p)
		if _, err := d.Handle(context.Background(), method, raw); err == nil || !strings.HasPrefix(err.Error(), method+": ") {
			t.Errorf("%s: err = %v, want a %q prefix", method, err, method+": ")
		}
	}
}

func TestGroupAdmin_NoPortIsMethodNotFound(t *testing.T) {
	t.Parallel()
	d, _ := groupAdminDispatcher(t, nil)
	for _, m := range []string{"group.create", "group.leave", "group.promote", "group.edit", "group.inviteGet", "group.inviteJoin"} {
		if _, err := d.Handle(context.Background(), m, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s with no port: no error", m)
		}
	}
}
