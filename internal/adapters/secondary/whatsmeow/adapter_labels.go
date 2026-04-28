package whatsmeow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// LabelsAdapter satisfies app.LabelManager against the whatsmeow Business
// appstate surface. Labels are written via three PatchInfo builders —
// BuildLabelEdit (create/delete/rename), BuildLabelChat, BuildLabelMessage —
// and then pushed through Client.SendAppState so the server can fan them
// out to the user's linked devices.
//
// whatsmeow does not expose a first-class query API for the server's label
// table: it only replays appstate mutations as events. We therefore carry a
// local write-through cache keyed by profile. The cache is authoritative for
// the current session; a restart rebuilds it from messages.db label rows as
// they come back through the appstate stream. The contract tests exercise
// the cache directly; the //go:build integration TestLabelRoundTrip30s
// exercises the full wire.
type LabelsAdapter struct {
	client whatsmeowClient
	nowFn  func() time.Time

	mu          sync.Mutex
	labels      map[labelKey]domain.Label
	labelOrder  map[labelKey]int64
	labelSeq    int64
	assigns     map[assignKey]domain.LabelAssignment
	assignOrder map[assignKey]int64
	assignSeq   int64
}

type labelKey struct {
	profile string
	id      string
}

type assignKey struct {
	profile   string
	labelID   string
	kind      domain.LabelTargetKind
	chat      domain.JID
	messageID domain.MessageID
}

// NewLabelsAdapter returns a LabelsAdapter wired to the Adapter's underlying
// whatsmeow client. nowFn is used for timestamp stamping; pass nil for
// time.Now.
func (a *Adapter) NewLabelsAdapter(nowFn func() time.Time) *LabelsAdapter {
	if nowFn == nil {
		nowFn = a.nowFn
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &LabelsAdapter{
		client:      a.client,
		nowFn:       nowFn,
		labels:      make(map[labelKey]domain.Label),
		labelOrder:  make(map[labelKey]int64),
		assigns:     make(map[assignKey]domain.LabelAssignment),
		assignOrder: make(map[assignKey]int64),
	}
}

// Compile-time assertion that LabelsAdapter satisfies app.LabelManager.
var _ app.LabelManager = (*LabelsAdapter)(nil)

// ListLabels returns the in-session cache for profile.
func (l *LabelsAdapter) ListLabels(ctx context.Context, profile string) ([]domain.Label, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	type indexed struct {
		label domain.Label
		seq   int64
	}
	matches := make([]indexed, 0, len(l.labels))
	for k, lbl := range l.labels {
		if k.profile != profile {
			continue
		}
		matches = append(matches, indexed{label: lbl, seq: l.labelOrder[k]})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]domain.Label, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.label)
	}
	return out, nil
}

// CreateLabel mints a fresh label id, pushes a BuildLabelEdit patch, and
// caches the persisted label. On SendAppState failure the cache is NOT
// updated and the error is surfaced — callers may retry.
func (l *LabelsAdapter) CreateLabel(ctx context.Context, profile, name string, colorIndex int) (domain.Label, error) {
	if err := ctx.Err(); err != nil {
		return domain.Label{}, err
	}
	id, err := mintLabelID()
	if err != nil {
		return domain.Label{}, fmt.Errorf("labels: mint id: %w", err)
	}
	lbl, err := domain.NewLabel(id, name, colorIndex, l.nowFn())
	if err != nil {
		return domain.Label{}, err
	}
	// #nosec G115 -- colorIndex is bounded to [0, LabelPaletteSize=20) by
	// domain.NewLabel above; int32 cast cannot overflow.
	patch := appstate.BuildLabelEdit(id, lbl.Name(), int32(colorIndex), false)
	if err := l.client.SendAppState(ctx, patch); err != nil {
		return domain.Label{}, fmt.Errorf("labels: send LabelEdit: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := labelKey{profile: profile, id: id}
	l.labelSeq++
	l.labels[k] = lbl
	l.labelOrder[k] = l.labelSeq
	return lbl, nil
}

// DeleteLabel pushes a BuildLabelEdit patch with deleted=true, then drops
// the label and every matching assignment from the cache. Idempotent on
// unknown ids: the patch is still sent (it is a no-op server-side).
func (l *LabelsAdapter) DeleteLabel(ctx context.Context, profile, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if labelID == "" {
		return nil
	}
	patch := appstate.BuildLabelEdit(labelID, "", 0, true)
	if err := l.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("labels: send LabelEdit(delete): %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := labelKey{profile: profile, id: labelID}
	delete(l.labels, k)
	delete(l.labelOrder, k)
	for ak := range l.assigns {
		if ak.profile == profile && ak.labelID == labelID {
			delete(l.assigns, ak)
			delete(l.assignOrder, ak)
		}
	}
	return nil
}

// Assign attaches a label to a chat or message. Dispatches to BuildLabelChat
// or BuildLabelMessage based on the assignment kind. Idempotent: repeated
// Assign for the same (labelID, target) returns nil.
func (l *LabelsAdapter) Assign(ctx context.Context, profile string, a domain.LabelAssignment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.sendAssignmentPatch(ctx, a, true); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := assignKey{
		profile:   profile,
		labelID:   a.LabelID(),
		kind:      a.TargetKind(),
		chat:      a.Chat(),
		messageID: a.MessageID(),
	}
	if _, ok := l.assigns[k]; ok {
		return nil
	}
	l.assignSeq++
	l.assigns[k] = a
	l.assignOrder[k] = l.assignSeq
	return nil
}

// Unassign detaches a label. Idempotent on missing rows.
func (l *LabelsAdapter) Unassign(ctx context.Context, profile string, a domain.LabelAssignment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.sendAssignmentPatch(ctx, a, false); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := assignKey{
		profile:   profile,
		labelID:   a.LabelID(),
		kind:      a.TargetKind(),
		chat:      a.Chat(),
		messageID: a.MessageID(),
	}
	delete(l.assigns, k)
	delete(l.assignOrder, k)
	return nil
}

// ListAssignments returns cached assignments for (profile, labelID).
func (l *LabelsAdapter) ListAssignments(ctx context.Context, profile, labelID string) ([]domain.LabelAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	type indexed struct {
		a   domain.LabelAssignment
		seq int64
	}
	matches := make([]indexed, 0, len(l.assigns))
	for k, a := range l.assigns {
		if k.profile != profile || k.labelID != labelID {
			continue
		}
		matches = append(matches, indexed{a: a, seq: l.assignOrder[k]})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]domain.LabelAssignment, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.a)
	}
	return out, nil
}

// sendAssignmentPatch dispatches to BuildLabelChat or BuildLabelMessage
// based on assignment kind, then pushes through SendAppState.
func (l *LabelsAdapter) sendAssignmentPatch(ctx context.Context, a domain.LabelAssignment, labeled bool) error {
	chat := toWhatsmeow(a.Chat())
	var patch appstate.PatchInfo
	switch a.TargetKind() {
	case domain.LabelTargetChat:
		patch = appstate.BuildLabelChat(chat, a.LabelID(), labeled)
	case domain.LabelTargetMessage:
		patch = appstate.BuildLabelMessage(chat, a.LabelID(), string(a.MessageID()), labeled)
	default:
		return fmt.Errorf("labels: unsupported assignment kind %v", a.TargetKind())
	}
	if err := l.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("labels: send LabelAssoc: %w", err)
	}
	return nil
}

// mintLabelID returns a 24-hex-char id derived from crypto/rand. WhatsApp
// Business accepts opaque label identifiers; the value only has to be
// stable for this session.
func mintLabelID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// DetectBusiness calls GetBusinessProfile for the paired device's own JID
// and returns true iff the server answers with a non-nil profile. A
// personal account returns a typed ElementMissingError from whatsmeow; we
// collapse that to (false, nil) so callers can act on the bool. Other
// errors (network, auth) propagate unchanged.
//
// Callers MUST persist the result via sqlitestore.SetIsBusiness so the
// flag survives restarts without re-issuing the IQ.
func (a *Adapter) DetectBusiness(ctx context.Context) (bool, error) {
	dev := a.client.Store()
	if dev == nil || dev.ID == nil {
		return false, fmt.Errorf("labels: DetectBusiness: device not paired")
	}
	ownJID := *dev.ID
	profile, err := a.client.GetBusinessProfile(ctx, ownJID.ToNonAD())
	if err != nil {
		// ElementMissingError (returned for personal accounts) means
		// "no business profile" — NOT an error from our perspective.
		if isElementMissing(err) {
			return false, nil
		}
		return false, err
	}
	if profile == nil {
		return false, nil
	}
	return true, nil
}

// isElementMissing sniffs for the whatsmeow *ElementMissingError wrapper
// without importing the type directly (it is in the whatsmeow package root
// and would introduce a cycle). The error message shape is stable since
// 2021.
func isElementMissing(err error) bool {
	// nil check for safety; callers already gate on err != nil.
	if err == nil {
		return false
	}
	msg := err.Error()
	// whatsmeow formats this error as:
	//   "<tag> missing in response to business profile query"
	// We match loosely on the "business_profile" substring which is
	// stable across whatsmeow versions.
	return containsBusinessProfileTag(msg)
}

func containsBusinessProfileTag(s string) bool {
	const needle = "business_profile"
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
