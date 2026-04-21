package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

type memDraftStore struct {
	mu   sync.Mutex
	rows map[string]domain.Draft
}

func newMemDraftStore() *memDraftStore { return &memDraftStore{rows: map[string]domain.Draft{}} }

func (s *memDraftStore) Put(ctx context.Context, d domain.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[d.ID] = d
	return nil
}

func (s *memDraftStore) Get(ctx context.Context, profile, id string) (domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.rows[id]
	if !ok {
		return domain.Draft{}, errors.New("not found")
	}
	return d, nil
}

func (s *memDraftStore) List(ctx context.Context, profile string, state domain.DraftState, limit int) ([]domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Draft{}
	for _, d := range s.rows {
		if d.Profile == profile && d.State == state {
			out = append(out, d)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memDraftStore) Approve(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider) (domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.rows[id]
	if !ok {
		return domain.Draft{}, errors.New("not found")
	}
	out, err := d.Approve(at.Unix(), by)
	if err != nil {
		return domain.Draft{}, err
	}
	s.rows[id] = out
	return out, nil
}

func (s *memDraftStore) Reject(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider, reason string) (domain.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.rows[id]
	if !ok {
		return domain.Draft{}, errors.New("not found")
	}
	out, err := d.Reject(at.Unix(), by, reason)
	if err != nil {
		return domain.Draft{}, err
	}
	s.rows[id] = out
	return out, nil
}

func (s *memDraftStore) ExpireDue(ctx context.Context, profile string, at time.Time) (int, error) {
	return 0, nil
}

func dispatchWithDrafts(store DraftStore, profile string) *Dispatcher {
	return &Dispatcher{drafts: store, profile: profile}
}

func TestDraftListPendingDefault(t *testing.T) {
	store := newMemDraftStore()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	d1, _ := domain.NewDraft("d1", "default", domain.DraftKindSend, `{"body":"hi"}`, jid, 1700000000)
	_ = store.Put(context.Background(), d1)

	disp := dispatchWithDrafts(store, "default")
	out, err := disp.handleDraftList(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got struct {
		Drafts []draftView `json:"drafts"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Drafts) != 1 || got.Drafts[0].ID != "d1" {
		t.Fatalf("drafts: %+v", got.Drafts)
	}
}

func TestDraftApproveOK(t *testing.T) {
	store := newMemDraftStore()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	d1, _ := domain.NewDraft("d1", "default", domain.DraftKindSend, `{"body":"hi"}`, jid, 1700000000)
	_ = store.Put(context.Background(), d1)

	disp := dispatchWithDrafts(store, "default")
	_, err := disp.handleDraftApprove(context.Background(), json.RawMessage(`{"id":"d1"}`))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _ := store.Get(context.Background(), "default", "d1")
	if got.State != domain.DraftApproved {
		t.Fatalf("state: %s", got.State)
	}
}

func TestDraftRejectRecordsReason(t *testing.T) {
	store := newMemDraftStore()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	d1, _ := domain.NewDraft("d1", "default", domain.DraftKindSend, `{"body":"hi"}`, jid, 1700000000)
	_ = store.Put(context.Background(), d1)

	disp := dispatchWithDrafts(store, "default")
	_, err := disp.handleDraftReject(context.Background(), json.RawMessage(`{"id":"d1","reason":"spam"}`))
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, _ := store.Get(context.Background(), "default", "d1")
	if got.State != domain.DraftRejected || got.Reason != "spam" {
		t.Fatalf("state=%s reason=%q", got.State, got.Reason)
	}
}

func TestDraftApproveRejectsTimeoutDecider(t *testing.T) {
	disp := dispatchWithDrafts(newMemDraftStore(), "default")
	_, err := disp.handleDraftApprove(context.Background(), json.RawMessage(`{"id":"d1","by":"timeout"}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err: got %v want ErrInvalidParams", err)
	}
}
