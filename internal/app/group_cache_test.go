package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

type stubGM struct {
	calls   atomic.Int64
	group   domain.Group
	listErr error
	getErr  error
}

func (s *stubGM) List(_ context.Context) ([]domain.Group, error) {
	return nil, s.listErr
}

func (s *stubGM) Get(_ context.Context, _ domain.JID) (domain.Group, error) {
	s.calls.Add(1)
	if s.getErr != nil {
		return domain.Group{}, s.getErr
	}
	return s.group, nil
}

func mustGroup(t *testing.T, jid domain.JID, subj string, ps []domain.JID, fetchedAt time.Time) domain.Group {
	t.Helper()
	g, err := domain.NewGroup(jid, subj, ps)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	g.FetchedAt = fetchedAt
	return g
}

// TestGroupCacheInvalidationOnInfo exercises FR-072/FR-073: fresh entries
// hit cache; Invalidate forces a refetch.
func TestGroupCacheInvalidationOnInfo(t *testing.T) {
	t.Parallel()

	jid := domain.MustJID("120363042199654321@g.us")
	ps := []domain.JID{domain.MustJID("5511999999999")}

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	clock := now
	tick := func() time.Time { return clock }

	stub := &stubGM{group: mustGroup(t, jid, "T", ps, now)}
	cache := app.NewGroupCache(stub, tick)

	if _, err := cache.Get(context.Background(), jid); err != nil {
		t.Fatalf("Get#1: %v", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("upstream calls after 1st Get = %d, want 1", got)
	}

	clock = now.Add(30 * time.Second)
	if _, err := cache.Get(context.Background(), jid); err != nil {
		t.Fatalf("Get#2: %v", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("upstream calls after warm Get = %d, want 1 (cache hit)", got)
	}

	cache.Invalidate(jid)
	if _, err := cache.Get(context.Background(), jid); err != nil {
		t.Fatalf("Get#3: %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("upstream calls after Invalidate = %d, want 2", got)
	}

	clock = now.Add(120 * time.Second)
	if _, err := cache.Get(context.Background(), jid); err != nil {
		t.Fatalf("Get#4: %v", err)
	}
	if got := stub.calls.Load(); got != 3 {
		t.Fatalf("upstream calls after TTL expiry = %d, want 3", got)
	}
}

func TestGroupCachePropagatesError(t *testing.T) {
	t.Parallel()
	jid := domain.MustJID("120363042199654321@g.us")
	sentinel := errors.New("upstream boom")
	cache := app.NewGroupCache(&stubGM{getErr: sentinel}, func() time.Time { return time.Unix(0, 0) })
	if _, err := cache.Get(context.Background(), jid); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if cache.Size() != 0 {
		t.Fatalf("failed Get must not populate cache, size=%d", cache.Size())
	}
}
