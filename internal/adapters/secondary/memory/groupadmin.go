package memory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// inviteURLPattern is the FR-025 canonical invite-URL shape.
var inviteURLPattern = regexp.MustCompile(`^https://chat\.whatsapp\.com/[A-Za-z0-9]+$`)

// GroupAdmin is the in-memory implementation of app.GroupAdmin (feature
// 018 FR-020..FR-025). The fake generates synthetic group JIDs of the
// form "1000000000-<seq>@g.us" so tests can pre-seed a counter by
// calling Create in a known order.
type GroupAdmin struct {
	mu         sync.Mutex
	groups     map[domain.JID]domain.Group
	invites    map[domain.JID]domain.GroupInviteLink
	inviteByID map[string]domain.JID
	createSeq  int
}

// NewGroupAdmin returns an empty fake.
func NewGroupAdmin() *GroupAdmin {
	return &GroupAdmin{
		groups:     make(map[domain.JID]domain.Group),
		invites:    make(map[domain.JID]domain.GroupInviteLink),
		inviteByID: make(map[string]domain.JID),
	}
}

// Create implements app.GroupAdmin.
func (g *GroupAdmin) Create(ctx context.Context, subject string, participants []domain.JID) (domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return domain.Group{}, err
	}
	if len(participants) == 0 {
		return domain.Group{}, fmt.Errorf("%w: group must have at least one participant", domain.ErrInvalidJID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.createSeq++
	jid, err := domain.Parse(fmt.Sprintf("1000000000-%d@g.us", g.createSeq))
	if err != nil {
		return domain.Group{}, fmt.Errorf("GroupAdmin.Create: synth jid: %w", err)
	}
	group, err := domain.NewGroup(jid, subject, participants)
	if err != nil {
		return domain.Group{}, fmt.Errorf("GroupAdmin.Create: %w", err)
	}
	g.groups[jid] = group
	return group, nil
}

// Leave implements app.GroupAdmin.
func (g *GroupAdmin) Leave(ctx context.Context, group domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !group.IsGroup() {
		return fmt.Errorf("%w: Leave requires a group JID", domain.ErrInvalidJID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.groups, group)
	return nil
}

// AddParticipants implements app.GroupAdmin.
func (g *GroupAdmin) AddParticipants(ctx context.Context, group domain.JID, add []domain.JID) error {
	return g.mutateRoster(ctx, group, add, "AddParticipants", func(cur []domain.JID, p domain.JID) []domain.JID {
		if slices.Contains(cur, p) {
			return cur
		}
		return append(cur, p)
	})
}

// RemoveParticipants implements app.GroupAdmin.
func (g *GroupAdmin) RemoveParticipants(ctx context.Context, group domain.JID, remove []domain.JID) error {
	return g.mutateRoster(ctx, group, remove, "RemoveParticipants", func(cur []domain.JID, p domain.JID) []domain.JID {
		idx := slices.Index(cur, p)
		if idx < 0 {
			return cur
		}
		return slices.Delete(cur, idx, idx+1)
	})
}

// Promote implements app.GroupAdmin.
func (g *GroupAdmin) Promote(ctx context.Context, group domain.JID, jids []domain.JID) error {
	return g.mutateAdmins(ctx, group, jids, "Promote", func(cur []domain.JID, p domain.JID) []domain.JID {
		if slices.Contains(cur, p) {
			return cur
		}
		return append(cur, p)
	})
}

// Demote implements app.GroupAdmin.
func (g *GroupAdmin) Demote(ctx context.Context, group domain.JID, jids []domain.JID) error {
	return g.mutateAdmins(ctx, group, jids, "Demote", func(cur []domain.JID, p domain.JID) []domain.JID {
		idx := slices.Index(cur, p)
		if idx < 0 {
			return cur
		}
		return slices.Delete(cur, idx, idx+1)
	})
}

// Edit implements app.GroupAdmin.
func (g *GroupAdmin) Edit(ctx context.Context, group domain.JID, patch domain.GroupPatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !group.IsGroup() {
		return fmt.Errorf("%w: Edit requires a group JID", domain.ErrInvalidJID)
	}
	if err := patch.Validate(); err != nil {
		return fmt.Errorf("GroupAdmin.Edit: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, ok := g.groups[group]
	if !ok {
		// The fake accepts edits against unseeded groups so the contract
		// runner's single-call smoke clause does not need to pre-seed
		// state it is not exercising.
		return nil
	}
	if patch.Subject != nil {
		cur.Subject = *patch.Subject
	}
	g.groups[group] = cur
	return nil
}

// InviteGet implements app.GroupAdmin.
func (g *GroupAdmin) InviteGet(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error) {
	if err := ctx.Err(); err != nil {
		return domain.GroupInviteLink{}, err
	}
	if !group.IsGroup() {
		return domain.GroupInviteLink{}, fmt.Errorf("%w: InviteGet requires a group JID", domain.ErrInvalidJID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if link, ok := g.invites[group]; ok {
		return link, nil
	}
	return g.mintInviteLocked(group), nil
}

// InviteRevoke implements app.GroupAdmin. Rotates to a fresh code.
func (g *GroupAdmin) InviteRevoke(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error) {
	if err := ctx.Err(); err != nil {
		return domain.GroupInviteLink{}, err
	}
	if !group.IsGroup() {
		return domain.GroupInviteLink{}, fmt.Errorf("%w: InviteRevoke requires a group JID", domain.ErrInvalidJID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if old, ok := g.invites[group]; ok {
		delete(g.inviteByID, old.Code)
	}
	return g.mintInviteLocked(group), nil
}

// InviteJoin implements app.GroupAdmin.
func (g *GroupAdmin) InviteJoin(ctx context.Context, url string) (domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return domain.Group{}, err
	}
	if !inviteURLPattern.MatchString(url) {
		return domain.Group{}, fmt.Errorf("GroupAdmin.InviteJoin: url %q does not match invite pattern", url)
	}
	code := url[len("https://chat.whatsapp.com/"):]
	g.mu.Lock()
	defer g.mu.Unlock()
	groupJID, ok := g.inviteByID[code]
	if !ok {
		return domain.Group{}, errors.New("GroupAdmin.InviteJoin: unknown invite code (upstream 404)")
	}
	return g.groups[groupJID], nil
}

// SeedGroup pre-populates a Group so methods that would otherwise mint
// one can return it. Test helper.
func (g *GroupAdmin) SeedGroup(gr domain.Group) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.groups[gr.JID] = gr
}

func (g *GroupAdmin) mutateRoster(ctx context.Context, group domain.JID, jids []domain.JID, method string, apply func([]domain.JID, domain.JID) []domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !group.IsGroup() {
		return fmt.Errorf("%w: %s requires a group JID", domain.ErrInvalidJID, method)
	}
	if len(jids) == 0 {
		return fmt.Errorf("GroupAdmin.%s: empty participant list", method)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, ok := g.groups[group]
	if !ok {
		return nil
	}
	for _, p := range jids {
		cur.Participants = apply(cur.Participants, p)
	}
	g.groups[group] = cur
	return nil
}

func (g *GroupAdmin) mutateAdmins(ctx context.Context, group domain.JID, jids []domain.JID, method string, apply func([]domain.JID, domain.JID) []domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !group.IsGroup() {
		return fmt.Errorf("%w: %s requires a group JID", domain.ErrInvalidJID, method)
	}
	if len(jids) == 0 {
		return fmt.Errorf("GroupAdmin.%s: empty jid list", method)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, ok := g.groups[group]
	if !ok {
		return nil
	}
	for _, p := range jids {
		cur.Admins = apply(cur.Admins, p)
	}
	g.groups[group] = cur
	return nil
}

func (g *GroupAdmin) mintInviteLocked(group domain.JID) domain.GroupInviteLink {
	code := "MEMINV" + strconv.Itoa(len(g.inviteByID)+1)
	link := domain.GroupInviteLink{
		Group: group,
		URL:   "https://chat.whatsapp.com/" + code,
		Code:  code,
	}
	g.invites[group] = link
	g.inviteByID[code] = group
	return link
}
