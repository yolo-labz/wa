# Contract: Rate Limiter + Warmup

**Feature**: 005-app-usecases

## Rate limiter

Three independent token buckets, checked sequentially for every outbound action.

| Bucket | Default rate | Default burst | Purpose |
|---|---|---|---|
| per-second | 2 tokens/s | 2 | Prevents burst flooding |
| per-minute | 0.5 tokens/s (30/min) | 30 | Prevents sustained high rate |
| per-day | ~0.012 tokens/s (1000/day) | 1000 | Prevents daily volume abuse |

### Allow check

```go
func (r *RateLimiter) Allow() error
```

Checks buckets in order: per-second → per-minute → per-day. If any returns false from `rate.Limiter.Allow()`, the request is rejected. If per-second passes but per-minute rejects, the per-second token is "wasted" — this is conservative (tighter than necessary), which is the safe direction for anti-ban.

Returns:
- `nil` — all three buckets passed
- `ErrRateLimited` — a bucket was exhausted (not warmup-related)
- `ErrWarmupActive` — a bucket was exhausted AND the warmup multiplier is < 1.0

The distinction between `ErrRateLimited` and `ErrWarmupActive` lets the client show a more helpful message ("your session is new, wait N days" vs "you're sending too fast").

### Thread safety

All three `rate.Limiter` instances are documented as safe for concurrent use. The `RateLimiter` struct adds no mutable state beyond the three limiters and the warmup multiplier (written only during recalculation, which is serialized).

## Warmup ramp

A pure function that computes the multiplier from session age:

```go
func warmupMultiplier(sessionCreated, now time.Time) float64
```

| Session age | Multiplier |
|---|---|
| < 7 days | 0.25 |
| 7-14 days | 0.50 |
| >= 14 days | 1.00 |

### Application to buckets

The multiplier scales both the rate AND the burst of each bucket:

| Bucket | 25% effective | 50% effective | 100% effective |
|---|---|---|---|
| per-second | 0.5/s, burst 1 | 1.0/s, burst 1 | 2.0/s, burst 2 |
| per-minute | ~0.125/s, burst 7 | ~0.25/s, burst 15 | ~0.5/s, burst 30 |
| per-day | ~0.003/s, burst 250 | ~0.006/s, burst 500 | ~0.012/s, burst 1000 |

Burst values are `max(1, int(defaultBurst * multiplier))` — never zero.

### Recalculation

The warmup multiplier is computed at construction and does NOT change during the dispatcher's lifetime. Rationale: the session age only crosses a threshold boundary once per week at most; restarting the daemon (which reconstructs the dispatcher) is sufficient to pick up the new multiplier. A future enhancement could add a daily ticker, but it is not in scope for v0.1.

### No override

There is no `--force` flag, no admin bypass, no per-request exemption. The rate limiter is non-bypassable per constitution principle III.

## Test contract

Every assertion below MUST be a passing test:

1. A fresh limiter allows exactly `burst` requests in an instant, then rejects the next.
2. After waiting `1/rate` seconds, one more request is allowed.
3. With warmup multiplier 0.25, the per-second burst is 1 (not 0).
4. With warmup multiplier 0.50, the per-second burst is 1 (0.5 * 2 = 1, rounded down but clamped to 1).
5. With warmup multiplier 1.0, the per-second burst is 2.
6. `warmupMultiplier` returns exactly 0.25/0.50/1.0 at the documented day boundaries.
7. Concurrent `Allow()` calls from 100 goroutines do not race (verified by `-race`).
