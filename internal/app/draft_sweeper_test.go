package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

type fakeDraftStore struct {
	mu      sync.Mutex
	drafts  map[string]domain.Draft
	expired []time.Time
}

func newFakeDraftStore() *fakeDraftStore {
	return &fakeDraftStore{drafts: map[string]domain.Draft{}}
}

func (s *fakeDraftStore) Put(ctx context.Context, d domain.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[d.ID] = d
	return nil
}

func (s *fakeDraftStore) Get(ctx context.Context, profile, id string) (domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[id]
	if !ok {
		return domain.Draft{}, context.DeadlineExceeded
	}
	return d, nil
}

func (s *fakeDraftStore) List(ctx context.Context, profile string, state domain.DraftState, limit int) ([]domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Draft{}
	for _, d := range s.drafts {
		if d.Profile == profile && d.State == state {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *fakeDraftStore) Approve(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (s *fakeDraftStore) Reject(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider, reason string) (domain.Draft, error) {
	return domain.Draft{}, nil
}

func (s *fakeDraftStore) ExpireDue(ctx context.Context, profile string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired = append(s.expired, at)
	n := 0
	for id, d := range s.drafts {
		if d.Profile != profile || d.State != domain.DraftPendingReview {
			continue
		}
		if at.Unix() < d.ExpiresAt {
			continue
		}
		next, err := d.ExpireAt(at.Unix())
		if err != nil {
			return 0, err
		}
		s.drafts[id] = next
		n++
	}
	return n, nil
}

func TestDraftExpiryAt30m(t *testing.T) {
	ctx := context.Background()
	store := newFakeDraftStore()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	baseTime := int64(1_700_000_000)
	d, err := domain.NewDraft("d1", "default", domain.DraftKindSend, `{"body":"hi"}`, jid, baseTime)
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	_ = store.Put(ctx, d)

	sw := NewDraftSweeper(store, "default")
	// 29 min elapsed: no sweep yet.
	sw.Now = func() time.Time { return time.Unix(baseTime+29*60, 0) }
	if n, err := sw.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("29m sweep: n=%d err=%v want n=0 err=nil", n, err)
	}
	// 31 min elapsed: past 30 min horizon → expire.
	sw.Now = func() time.Time { return time.Unix(baseTime+31*60, 0) }
	if n, err := sw.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("31m sweep: n=%d err=%v want n=1 err=nil", n, err)
	}
	got, _ := store.Get(ctx, "default", "d1")
	if got.State != domain.DraftExpired {
		t.Fatalf("state: got %v want expired", got.State)
	}
}
