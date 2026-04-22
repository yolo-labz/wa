package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	waClient "go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// Hard daily caps enforced inside the adapter per ports_018.go and
// CLAUDE.md §Safety. These are the belt on top of the send-path rate
// limiter: even if the operator's send budget is still healthy, a
// day-scoped Create counter refuses after 5 and an Add counter refuses
// after 50. Refusal returns app.ErrRateLimitedHard so the dispatcher
// surfaces -32200 on the wire.
const (
	maxGroupCreatesPerDay    = 5
	maxParticipantAddsPerDay = 50
	adapterGroupNameMaxBytes = 25 // WhatsApp server-side cap; 406 otherwise
)

// GroupAdminAdapter is the whatsmeow-backed implementation of
// app.GroupAdmin (FR-020..FR-025). This file implements the T2-15
// subset — Create + Leave. T2-16..T2-18 layer on AddParticipants /
// Promote / Edit / Invite on the same struct.
//
// The per-day counters live here (not in app.RateLimiter) because they
// are domain-hard caps rather than per-recipient send throttles — a
// successful Create consumes a Create budget slot regardless of how
// many sends the operator has done today.
type GroupAdminAdapter struct {
	client whatsmeowClient
	audit  *auditRingBuffer
	logger *slog.Logger
	nowFn  func() time.Time

	mu          sync.Mutex
	createDay   time.Time // UTC midnight of the day the counters reflect
	createCount int
	addDay      time.Time
	addCount    int
}

// NewGroupAdminFor wires a GroupAdminAdapter to the Adapter's whatsmeow
// client, audit ring, logger, and clock.
func (a *Adapter) NewGroupAdminFor() (*GroupAdminAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewGroupAdminFor: nil adapter/client")
	}
	nowFn := a.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	return &GroupAdminAdapter{
		client: a.client,
		audit:  a.auditBuf,
		logger: a.logger,
		nowFn:  nowFn,
	}, nil
}

// Create implements app.GroupAdmin. Subject is capped at 25 bytes
// client-side (WhatsApp replies 406 on overflow). The ≤5 groups/day
// adapter-hard cap is checked BEFORE the upstream IQ so a budget
// refusal never burns a slot. Writes AuditGroupCreate on success.
func (g *GroupAdminAdapter) Create(ctx context.Context, subject string, participants []domain.JID) (domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return domain.Group{}, err
	}
	if len(subject) == 0 {
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w: empty subject", domain.ErrEmptyBody)
	}
	if len(subject) > adapterGroupNameMaxBytes {
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w: subject %d > %d bytes",
			domain.ErrMessageTooLarge, len(subject), adapterGroupNameMaxBytes)
	}
	if len(participants) == 0 {
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w: no participants", domain.ErrInvalidJID)
	}
	for i, p := range participants {
		if p.IsZero() {
			return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w: participant[%d] zero", domain.ErrInvalidJID, i)
		}
		if !p.IsUser() {
			return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w: participant[%d]=%q not a user JID",
				domain.ErrInvalidJID, i, p.String())
		}
	}
	if !g.client.IsConnected() {
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w", domain.ErrDisconnected)
	}

	if err := g.reserveCreateSlot(); err != nil {
		return domain.Group{}, err
	}

	waParts := make([]waTypes.JID, 0, len(participants))
	for _, p := range participants {
		waParts = append(waParts, toWhatsmeow(p))
	}
	info, err := g.client.CreateGroup(ctx, waClient.ReqCreateGroup{
		Name:         subject,
		Participants: waParts,
	})
	if err != nil {
		// Roll back the reservation so a transport failure does not
		// permanently burn a daily slot. The caller sees the raw error
		// and can retry without hitting the cap.
		g.refundCreateSlot()
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: %w", err)
	}
	out, err := translateGroup(info)
	if err != nil {
		g.refundCreateSlot()
		return domain.Group{}, fmt.Errorf("GroupAdminAdapter.Create: translate: %w", err)
	}
	g.recordAudit(domain.AuditGroupCreate, out.JID, "ok", subject)
	return out, nil
}

// Leave implements app.GroupAdmin. Writes AuditLeaveGroup on success.
// Not rate-limited: leaving a group is a tidy-up operation the user
// may do in bursts when pruning.
func (g *GroupAdminAdapter) Leave(ctx context.Context, group domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if group.IsZero() {
		return fmt.Errorf("GroupAdminAdapter.Leave: %w", domain.ErrInvalidJID)
	}
	if !group.IsGroup() {
		return fmt.Errorf("GroupAdminAdapter.Leave: %w: %s is not a group JID", domain.ErrInvalidJID, group.String())
	}
	if !g.client.IsConnected() {
		return fmt.Errorf("GroupAdminAdapter.Leave: %w", domain.ErrDisconnected)
	}
	if err := g.client.LeaveGroup(ctx, toWhatsmeow(group)); err != nil {
		return fmt.Errorf("GroupAdminAdapter.Leave: %w", err)
	}
	g.recordAudit(domain.AuditLeaveGroup, group, "ok", "")
	return nil
}

// Edit, InviteGet, InviteRevoke, InviteJoin are T2-17 / T2-18 territory.
// Stubbed here so the struct satisfies app.GroupAdmin for composition
// root wiring; the later tasks replace each body with a real impl.

// AddParticipants implements app.GroupAdmin. Enforces the ≤ 50 adds/day
// adapter-hard cap BEFORE the upstream IQ so a budget refusal never
// burns an Add slot. Refunds the slot if the wire call fails so transient
// errors do not permanently consume budget. Admin-only refusals (403) are
// mapped to domain.ErrNotAdmin (→ -32100 policy_refused). Partial
// successes — where upstream returns 200 but individual participants
// carry non-zero .Error codes — surface as a combined error listing the
// offending JIDs + codes so the caller can see which participants failed.
// Writes AuditGroupParticipantsPatch on success.
func (g *GroupAdminAdapter) AddParticipants(ctx context.Context, group domain.JID, add []domain.JID) error {
	if err := g.validateRosterInput(ctx, group, add); err != nil {
		return fmt.Errorf("GroupAdminAdapter.AddParticipants: %w", err)
	}
	if err := g.reserveAddSlots(len(add)); err != nil {
		return err
	}
	if err := g.updateParticipants(ctx, group, add, waClient.ParticipantChangeAdd); err != nil {
		g.refundAddSlots(len(add))
		return fmt.Errorf("GroupAdminAdapter.AddParticipants: %w", err)
	}
	g.recordAudit(domain.AuditGroupParticipantsPatch, group, "ok", "add:"+jidListDetail(add))
	return nil
}

// RemoveParticipants implements app.GroupAdmin. Not rate-limited:
// removal is a tidy-up that must remain unblocked so an operator can
// eject a spammer even after the Add budget is exhausted.
func (g *GroupAdminAdapter) RemoveParticipants(ctx context.Context, group domain.JID, remove []domain.JID) error {
	if err := g.validateRosterInput(ctx, group, remove); err != nil {
		return fmt.Errorf("GroupAdminAdapter.RemoveParticipants: %w", err)
	}
	if err := g.updateParticipants(ctx, group, remove, waClient.ParticipantChangeRemove); err != nil {
		return fmt.Errorf("GroupAdminAdapter.RemoveParticipants: %w", err)
	}
	g.recordAudit(domain.AuditGroupParticipantsPatch, group, "ok", "remove:"+jidListDetail(remove))
	return nil
}

// Promote implements app.GroupAdmin. Elevates the listed participants
// to admin. Upstream must be admin itself; "not admin" returns
// domain.ErrNotAdmin (→ -32100 policy_refused).
func (g *GroupAdminAdapter) Promote(ctx context.Context, group domain.JID, jids []domain.JID) error {
	if err := g.validateRosterInput(ctx, group, jids); err != nil {
		return fmt.Errorf("GroupAdminAdapter.Promote: %w", err)
	}
	if err := g.updateParticipants(ctx, group, jids, waClient.ParticipantChangePromote); err != nil {
		return fmt.Errorf("GroupAdminAdapter.Promote: %w", err)
	}
	g.recordAudit(domain.AuditGroupParticipantsPatch, group, "ok", "promote:"+jidListDetail(jids))
	return nil
}

// Demote implements app.GroupAdmin. Inverse of Promote.
func (g *GroupAdminAdapter) Demote(ctx context.Context, group domain.JID, jids []domain.JID) error {
	if err := g.validateRosterInput(ctx, group, jids); err != nil {
		return fmt.Errorf("GroupAdminAdapter.Demote: %w", err)
	}
	if err := g.updateParticipants(ctx, group, jids, waClient.ParticipantChangeDemote); err != nil {
		return fmt.Errorf("GroupAdminAdapter.Demote: %w", err)
	}
	g.recordAudit(domain.AuditGroupParticipantsPatch, group, "ok", "demote:"+jidListDetail(jids))
	return nil
}

// Edit is a T2-17 stub; see app.GroupAdmin for the contract.
func (g *GroupAdminAdapter) Edit(ctx context.Context, group domain.JID, patch domain.GroupPatch) error {
	return errors.New("GroupAdminAdapter.Edit: not implemented (T2-17)")
}

// InviteGet is a T2-18 stub; see app.GroupAdmin for the contract.
func (g *GroupAdminAdapter) InviteGet(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, errors.New("GroupAdminAdapter.InviteGet: not implemented (T2-18)")
}

// InviteRevoke is a T2-18 stub; see app.GroupAdmin for the contract.
func (g *GroupAdminAdapter) InviteRevoke(ctx context.Context, group domain.JID) (domain.GroupInviteLink, error) {
	return domain.GroupInviteLink{}, errors.New("GroupAdminAdapter.InviteRevoke: not implemented (T2-18)")
}

// InviteJoin is a T2-18 stub; see app.GroupAdmin for the contract.
func (g *GroupAdminAdapter) InviteJoin(ctx context.Context, url string) (domain.Group, error) {
	return domain.Group{}, errors.New("GroupAdminAdapter.InviteJoin: not implemented (T2-18)")
}

// reserveCreateSlot consumes one daily Create slot, rolling the day
// counter at UTC midnight. Returns app.ErrRateLimitedHard when the
// budget is exhausted.
func (g *GroupAdminAdapter) reserveCreateSlot() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	today := g.nowFn().UTC().Truncate(24 * time.Hour)
	if !g.createDay.Equal(today) {
		g.createDay = today
		g.createCount = 0
	}
	if g.createCount >= maxGroupCreatesPerDay {
		return fmt.Errorf("GroupAdminAdapter.Create: %w: %d groups/day cap", app.ErrRateLimitedHard, maxGroupCreatesPerDay)
	}
	g.createCount++
	return nil
}

// refundCreateSlot undoes a reservation on upstream failure so transient
// errors do not burn a permanent slot.
func (g *GroupAdminAdapter) refundCreateSlot() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.createCount > 0 {
		g.createCount--
	}
}

func (g *GroupAdminAdapter) recordAudit(action domain.AuditAction, subject domain.JID, decision, detail string) {
	if g.audit == nil {
		return
	}
	_ = g.audit.Record(context.Background(), domain.AuditEvent{
		TS:       g.nowFn(),
		Actor:    "whatsmeow",
		Action:   action,
		Subject:  subject,
		Decision: decision,
		Detail:   detail,
	})
}

// validateRosterInput is the shared precondition check for AddParticipants,
// RemoveParticipants, Promote, Demote. It rejects the obvious shape errors
// before we reserve any budget slot.
func (g *GroupAdminAdapter) validateRosterInput(ctx context.Context, group domain.JID, jids []domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if group.IsZero() {
		return fmt.Errorf("%w: group zero", domain.ErrInvalidJID)
	}
	if !group.IsGroup() {
		return fmt.Errorf("%w: %s is not a group JID", domain.ErrInvalidJID, group.String())
	}
	if len(jids) == 0 {
		return fmt.Errorf("%w: empty participant list", domain.ErrInvalidJID)
	}
	for i, p := range jids {
		if p.IsZero() {
			return fmt.Errorf("%w: participant[%d] zero", domain.ErrInvalidJID, i)
		}
		if !p.IsUser() {
			return fmt.Errorf("%w: participant[%d]=%q not a user JID", domain.ErrInvalidJID, i, p.String())
		}
	}
	if !g.client.IsConnected() {
		return domain.ErrDisconnected
	}
	return nil
}

// reserveAddSlots consumes n daily Add slots, rolling the day counter at
// UTC midnight. Returns app.ErrRateLimitedHard when the budget would
// overflow the 50/day cap.
func (g *GroupAdminAdapter) reserveAddSlots(n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	today := g.nowFn().UTC().Truncate(24 * time.Hour)
	if !g.addDay.Equal(today) {
		g.addDay = today
		g.addCount = 0
	}
	if g.addCount+n > maxParticipantAddsPerDay {
		return fmt.Errorf("GroupAdminAdapter.AddParticipants: %w: %d adds/day cap (have %d, want %d)",
			app.ErrRateLimitedHard, maxParticipantAddsPerDay, g.addCount, n)
	}
	g.addCount += n
	return nil
}

// refundAddSlots returns n slots to the current day's Add budget so a
// transient upstream failure does not permanently burn budget.
func (g *GroupAdminAdapter) refundAddSlots(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addCount -= n
	if g.addCount < 0 {
		g.addCount = 0
	}
}

// updateParticipants is the single upstream entry point for Add / Remove /
// Promote / Demote. Maps whatsmeow ErrIQForbidden / ErrIQNotAllowed to
// domain.ErrNotAdmin so the socket layer surfaces -32100 policy_refused.
// Walks the returned slice for per-participant .Error codes (partial
// success) and combines the failures into a single error listing the
// offending JID + code pairs.
func (g *GroupAdminAdapter) updateParticipants(ctx context.Context, group domain.JID, jids []domain.JID, action waClient.ParticipantChange) error {
	waJIDs := make([]waTypes.JID, 0, len(jids))
	for _, j := range jids {
		waJIDs = append(waJIDs, toWhatsmeow(j))
	}
	result, err := g.client.UpdateGroupParticipants(ctx, toWhatsmeow(group), waJIDs, action)
	if err != nil {
		if errors.Is(err, waClient.ErrIQForbidden) || errors.Is(err, waClient.ErrIQNotAllowed) {
			return fmt.Errorf("%w: %v", domain.ErrNotAdmin, err)
		}
		return err
	}
	var failed []string
	for _, p := range result {
		if p.Error != 0 {
			failed = append(failed, p.JID.String()+":"+strconv.Itoa(p.Error))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: per-participant errors: %v", domain.ErrUpstreamError, failed)
	}
	return nil
}

// jidListDetail formats a JID slice for the audit Detail field. Clips
// long lists to keep audit log lines bounded.
func jidListDetail(jids []domain.JID) string {
	const maxShown = 5
	if len(jids) == 0 {
		return ""
	}
	n := len(jids)
	shown := n
	if shown > maxShown {
		shown = maxShown
	}
	out := ""
	for i := 0; i < shown; i++ {
		if i > 0 {
			out += ","
		}
		out += jids[i].String()
	}
	if n > maxShown {
		out += " +" + strconv.Itoa(n-maxShown) + " more"
	}
	return out
}
