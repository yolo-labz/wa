package app

import (
	"context"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// GroupCacheTTL is the freshness window for cached Group metadata per
// FR-072/FR-073. Entries older than this are treated as a miss.
const GroupCacheTTL = 60 * time.Second

// GroupCache wraps a GroupManager with a TTL-bounded in-process cache.
// It is safe for concurrent use and invalidated explicitly via Invalidate,
// which the whatsmeow adapter calls on every events.GroupInfo signal.
//
// The cache is intentionally small: participant rosters rarely exceed
// 1024 entries and memory pressure is negligible on the daemon host.
type GroupCache struct {
	upstream GroupManager
	now      func() time.Time
	ttl      time.Duration

	mu      sync.Mutex
	entries map[domain.JID]groupCacheEntry
}

type groupCacheEntry struct {
	group     domain.Group
	fetchedAt time.Time
}

// NewGroupCache returns a cache around upstream. If now is nil,
// time.Now is used.
func NewGroupCache(upstream GroupManager, now func() time.Time) *GroupCache {
	if now == nil {
		now = time.Now
	}
	return &GroupCache{
		upstream: upstream,
		now:      now,
		ttl:      GroupCacheTTL,
		entries:  make(map[domain.JID]groupCacheEntry),
	}
}

// List delegates to the upstream GroupManager without caching the result:
// group listings are infrequent and fully hydrating them into the cache
// would shadow fresher per-group fetches.
func (c *GroupCache) List(ctx context.Context) ([]domain.Group, error) {
	return c.upstream.List(ctx)
}

// Get returns the group for jid, serving from cache when an entry younger
// than TTL exists. Misses fall through to the upstream and populate the
// cache with the upstream's FetchedAt (or the local clock if unset).
func (c *GroupCache) Get(ctx context.Context, jid domain.JID) (domain.Group, error) {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[jid]; ok && now.Sub(e.fetchedAt) <= c.ttl {
		g := e.group
		c.mu.Unlock()
		return g, nil
	}
	c.mu.Unlock()

	g, err := c.upstream.Get(ctx, jid)
	if err != nil {
		return domain.Group{}, err
	}
	fetchedAt := g.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = now
		g.FetchedAt = now
	}
	c.mu.Lock()
	c.entries[jid] = groupCacheEntry{group: g, fetchedAt: fetchedAt}
	c.mu.Unlock()
	return g, nil
}

// Invalidate drops the cached entry for jid. Safe to call for an unknown
// jid (no-op). The whatsmeow adapter wires this to events.GroupInfo.
func (c *GroupCache) Invalidate(jid domain.JID) {
	c.mu.Lock()
	delete(c.entries, jid)
	c.mu.Unlock()
}

// Size reports how many entries are currently cached. Test-only.
func (c *GroupCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
