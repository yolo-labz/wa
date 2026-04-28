package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Blocker is the in-memory implementation of app.Blocker (feature 018
// FR-018/FR-019).
type Blocker struct {
	mu      sync.Mutex
	blocked map[domain.JID]struct{}
}

// NewBlocker returns an empty fake.
func NewBlocker() *Blocker {
	return &Blocker{blocked: make(map[domain.JID]struct{})}
}

// Block implements app.Blocker. Idempotent.
func (b *Blocker) Block(ctx context.Context, jid domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jid.IsZero() {
		return fmt.Errorf("Blocker.Block: %w", domain.ErrInvalidJID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[jid] = struct{}{}
	return nil
}

// Unblock implements app.Blocker. Unblocking a non-blocked JID is a no-op
// that returns nil.
func (b *Blocker) Unblock(ctx context.Context, jid domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jid.IsZero() {
		return fmt.Errorf("Blocker.Unblock: %w", domain.ErrInvalidJID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blocked, jid)
	return nil
}

// ListBlocked implements app.Blocker. Returns JIDs sorted by String()
// so contract assertions stay deterministic.
func (b *Blocker) ListBlocked(ctx context.Context) ([]domain.JID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]domain.JID, 0, len(b.blocked))
	for j := range b.blocked {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// Contains reports whether jid is currently blocked. Test accessor.
func (b *Blocker) Contains(jid domain.JID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.blocked[jid]
	return ok
}

// ContainsAny reports whether any of the given JIDs are blocked. Used by
// the suite-level sender gate check if wired.
func (b *Blocker) ContainsAny(jids []domain.JID) bool {
	blocked, err := b.ListBlocked(context.Background())
	if err != nil {
		return false
	}
	for _, j := range jids {
		if slices.Contains(blocked, j) {
			return true
		}
	}
	return false
}
