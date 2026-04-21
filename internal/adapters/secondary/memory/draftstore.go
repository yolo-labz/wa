package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// DraftStore is the in-memory implementation of app.DraftStore per
// FR-040..FR-045.
type DraftStore struct {
	mu     sync.Mutex
	rows   map[draftRowKey]domain.Draft
	putSeq int64
	order  map[draftRowKey]int64
}

type draftRowKey struct {
	profile string
	id      string
}

// NewDraftStore returns an empty in-memory DraftStore.
func NewDraftStore() *DraftStore {
	return &DraftStore{
		rows:  make(map[draftRowKey]domain.Draft),
		order: make(map[draftRowKey]int64),
	}
}

// Put implements app.DraftStore.
func (s *DraftStore) Put(ctx context.Context, d domain.Draft) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := draftRowKey{profile: d.Profile, id: d.ID}
	if _, exists := s.order[k]; !exists {
		s.putSeq++
		s.order[k] = s.putSeq
	}
	s.rows[k] = d
	return nil
}

// Get implements app.DraftStore.
func (s *DraftStore) Get(ctx context.Context, profile, id string) (domain.Draft, error) {
	if err := ctx.Err(); err != nil {
		return domain.Draft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.rows[draftRowKey{profile: profile, id: id}]
	if !ok {
		return domain.Draft{}, fmt.Errorf("%w: draft %s/%s", ErrNotFound, profile, id)
	}
	return d, nil
}

// List implements app.DraftStore. Returns drafts in insertion order,
// filtered by state, capped at limit.
func (s *DraftStore) List(ctx context.Context, profile string, state domain.DraftState, limit int) ([]domain.Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit %d", ErrInvalidLimit, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type indexed struct {
		draft domain.Draft
		seq   int64
	}
	matches := make([]indexed, 0, len(s.rows))
	for k, d := range s.rows {
		if k.profile != profile {
			continue
		}
		if d.State != state {
			continue
		}
		matches = append(matches, indexed{draft: d, seq: s.order[k]})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]domain.Draft, 0, len(matches))
	for i, m := range matches {
		if i >= limit {
			break
		}
		out = append(out, m.draft)
	}
	return out, nil
}

// Approve implements app.DraftStore.
func (s *DraftStore) Approve(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider) (domain.Draft, error) {
	return s.transition(ctx, profile, id, func(d domain.Draft) (domain.Draft, error) {
		return d.Approve(at.Unix(), by)
	})
}

// Reject implements app.DraftStore.
func (s *DraftStore) Reject(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider, reason string) (domain.Draft, error) {
	return s.transition(ctx, profile, id, func(d domain.Draft) (domain.Draft, error) {
		return d.Reject(at.Unix(), by, reason)
	})
}

// ExpireDue implements app.DraftStore. Returns the count of drafts moved
// from pending_review → expired.
func (s *DraftStore) ExpireDue(ctx context.Context, profile string, at time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, d := range s.rows {
		if k.profile != profile || d.State != domain.DraftPendingReview {
			continue
		}
		if at.Unix() < d.ExpiresAt {
			continue
		}
		expired, err := d.ExpireAt(at.Unix())
		if err != nil {
			return n, err
		}
		s.rows[k] = expired
		n++
	}
	return n, nil
}

func (s *DraftStore) transition(ctx context.Context, profile, id string, fn func(domain.Draft) (domain.Draft, error)) (domain.Draft, error) {
	if err := ctx.Err(); err != nil {
		return domain.Draft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := draftRowKey{profile: profile, id: id}
	d, ok := s.rows[k]
	if !ok {
		return domain.Draft{}, fmt.Errorf("%w: draft %s/%s", ErrNotFound, profile, id)
	}
	next, err := fn(d)
	if err != nil {
		return domain.Draft{}, err
	}
	s.rows[k] = next
	return next, nil
}
