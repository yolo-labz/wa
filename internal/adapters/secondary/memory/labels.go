package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// LabelManager is the in-memory implementation of app.LabelManager per
// FR-120..FR-122. It is used by tests and by the `--dry-run` daemon mode;
// the production path is internal/adapters/secondary/whatsmeow.
type LabelManager struct {
	mu          sync.Mutex
	labels      map[labelRowKey]domain.Label
	labelOrder  map[labelRowKey]int64
	labelSeq    int64
	assigns     map[assignRowKey]domain.LabelAssignment
	assignOrder map[assignRowKey]int64
	assignSeq   int64
	idSeq       int64
	clock       func() time.Time
}

type labelRowKey struct {
	profile string
	id      string
}

type assignRowKey struct {
	profile   string
	labelID   string
	kind      domain.LabelTargetKind
	chat      domain.JID
	messageID domain.MessageID
}

// NewLabelManager returns an empty in-memory LabelManager. clock is used
// for CreatedAt/UpdatedAt stamps; pass nil to use time.Now.
func NewLabelManager(clock func() time.Time) *LabelManager {
	if clock == nil {
		clock = time.Now
	}
	return &LabelManager{
		labels:      make(map[labelRowKey]domain.Label),
		labelOrder:  make(map[labelRowKey]int64),
		assigns:     make(map[assignRowKey]domain.LabelAssignment),
		assignOrder: make(map[assignRowKey]int64),
		clock:       clock,
	}
}

// ListLabels implements app.LabelManager.
func (m *LabelManager) ListLabels(ctx context.Context, profile string) ([]domain.Label, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type indexed struct {
		label domain.Label
		seq   int64
	}
	matches := make([]indexed, 0, len(m.labels))
	for k, l := range m.labels {
		if k.profile != profile {
			continue
		}
		matches = append(matches, indexed{label: l, seq: m.labelOrder[k]})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]domain.Label, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.label)
	}
	return out, nil
}

// CreateLabel implements app.LabelManager.
func (m *LabelManager) CreateLabel(ctx context.Context, profile, name string, colorIndex int) (domain.Label, error) {
	if err := ctx.Err(); err != nil {
		return domain.Label{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idSeq++
	id := fmt.Sprintf("lbl_%d", m.idSeq)
	l, err := domain.NewLabel(id, name, colorIndex, m.clock())
	if err != nil {
		return domain.Label{}, err
	}
	k := labelRowKey{profile: profile, id: id}
	m.labelSeq++
	m.labels[k] = l
	m.labelOrder[k] = m.labelSeq
	return l, nil
}

// DeleteLabel implements app.LabelManager. Idempotent: unknown ids return
// nil. Cascading assignment removal keeps the fake's invariants tidy.
func (m *LabelManager) DeleteLabel(ctx context.Context, profile, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := labelRowKey{profile: profile, id: labelID}
	delete(m.labels, k)
	delete(m.labelOrder, k)
	for ak := range m.assigns {
		if ak.profile == profile && ak.labelID == labelID {
			delete(m.assigns, ak)
			delete(m.assignOrder, ak)
		}
	}
	return nil
}

// Assign implements app.LabelManager. Idempotent on (labelID, target).
func (m *LabelManager) Assign(ctx context.Context, profile string, a domain.LabelAssignment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := assignRowKey{
		profile:   profile,
		labelID:   a.LabelID(),
		kind:      a.TargetKind(),
		chat:      a.Chat(),
		messageID: a.MessageID(),
	}
	if _, ok := m.assigns[k]; ok {
		return nil
	}
	m.assignSeq++
	m.assigns[k] = a
	m.assignOrder[k] = m.assignSeq
	return nil
}

// Unassign implements app.LabelManager. Idempotent on missing rows.
func (m *LabelManager) Unassign(ctx context.Context, profile string, a domain.LabelAssignment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := assignRowKey{
		profile:   profile,
		labelID:   a.LabelID(),
		kind:      a.TargetKind(),
		chat:      a.Chat(),
		messageID: a.MessageID(),
	}
	delete(m.assigns, k)
	delete(m.assignOrder, k)
	return nil
}

// ListAssignments implements app.LabelManager.
func (m *LabelManager) ListAssignments(ctx context.Context, profile, labelID string) ([]domain.LabelAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type indexed struct {
		a   domain.LabelAssignment
		seq int64
	}
	matches := make([]indexed, 0, len(m.assigns))
	for k, a := range m.assigns {
		if k.profile != profile || k.labelID != labelID {
			continue
		}
		matches = append(matches, indexed{a: a, seq: m.assignOrder[k]})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]domain.LabelAssignment, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.a)
	}
	return out, nil
}
