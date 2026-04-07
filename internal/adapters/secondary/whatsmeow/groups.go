package whatsmeow

import (
	"context"
	"fmt"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

// List implements app.GroupManager. It merges the overlay with any
// groups the whatsmeow client reports via GetJoinedGroups, preferring
// the overlay entry on JID collision.
//
// Per ports.go §GroupManager: returns an empty (non-nil) slice on an
// empty store and nil error.
func (a *Adapter) List(ctx context.Context) ([]domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	merged := make(map[domain.JID]domain.Group)

	// Upstream groups first; overlay overwrites on collision.
	if g, err := a.client.GetJoinedGroups(ctx); err == nil {
		for _, info := range g {
			dg, err := translateGroup(info)
			if err != nil {
				// Skip groups that fail translation rather than fail
				// the whole call; the translation error is surfaced
				// via the audit log for visibility.
				a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "group_translate", err.Error())
				continue
			}
			merged[dg.JID] = dg
		}
	}

	a.overlayMu.Lock()
	for jid, g := range a.seedGroups {
		merged[jid] = g
	}
	a.overlayMu.Unlock()

	out := make([]domain.Group, 0, len(merged))
	for _, g := range merged {
		out = append(out, g)
	}
	return out, nil
}

// Get implements app.GroupManager. It consults the overlay first, then
// falls back to GetGroupInfo.
func (a *Adapter) Get(ctx context.Context, jid domain.JID) (domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return domain.Group{}, err
	}
	if !jid.IsGroup() {
		return domain.Group{}, fmt.Errorf("GroupManager.Get: %w: %s is not a group JID", domain.ErrInvalidJID, jid)
	}

	a.overlayMu.Lock()
	if g, ok := a.seedGroups[jid]; ok {
		a.overlayMu.Unlock()
		return g, nil
	}
	a.overlayMu.Unlock()

	info, err := a.client.GetGroupInfo(ctx, toWhatsmeow(jid))
	if err != nil {
		return domain.Group{}, fmt.Errorf("%w: %s: %v", ErrNotFound, jid, err)
	}
	return translateGroup(info)
}

// translateGroup maps a whatsmeow *types.GroupInfo into a domain.Group,
// extracting only the participants' primary JIDs.
func translateGroup(info *waTypes.GroupInfo) (domain.Group, error) {
	if info == nil {
		return domain.Group{}, fmt.Errorf("%w: nil GroupInfo", domain.ErrInvalidJID)
	}
	jid, err := toDomain(info.JID)
	if err != nil {
		return domain.Group{}, fmt.Errorf("group jid: %w", err)
	}
	participants := make([]domain.JID, 0, len(info.Participants))
	admins := make([]domain.JID, 0)
	for _, p := range info.Participants {
		pj, err := toDomain(p.JID)
		if err != nil {
			continue
		}
		if !pj.IsUser() {
			continue
		}
		participants = append(participants, pj)
		if p.IsAdmin || p.IsSuperAdmin {
			admins = append(admins, pj)
		}
	}
	if len(participants) == 0 {
		// NewGroup rejects zero-participant groups; return a bare struct.
		return domain.Group{
			JID:     jid,
			Subject: info.Name,
		}, nil
	}
	g, err := domain.NewGroup(jid, info.Name, participants)
	if err != nil {
		return domain.Group{}, err
	}
	g.Admins = admins
	return g, nil
}
