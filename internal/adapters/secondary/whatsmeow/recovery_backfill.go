package whatsmeow

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// recoveryCooldown bounds deferred pulls to one attempt per chat per window.
const recoveryCooldown = 5 * time.Minute

// RecordPull stores one measured deferred-pull outcome against an episode.
type RecordPull func(ctx context.Context, chat string, gen uint64, result string, received int) error

// RecoveryBackfill runs FR-116-24 deferred pulls. Each episode's first real
// live anchor (OnAnchor, edge-triggered) starts at most one capped pull of
// historyRoundTripCap messages before that exact message, queued until the
// chat's cooldown expires and admitted by the daemon-wide pull budget.
// Disabled means detect-only: episodes stay visible and nothing is sent. It
// never reconnects, logs out, retries in a loop or reports success it did
// not measure; shutdown leaves the episode pending for the next start.
type RecoveryBackfill struct {
	a       *Adapter
	record  RecordPull
	enabled bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	last    map[string]time.Time
	busy    map[string]bool
	invalid atomic.Uint64 // anchored episodes with an unparsable chat (S1b review F8)
}

// NewRecoveryBackfill builds the worker; Close stops it.
func NewRecoveryBackfill(a *Adapter, record RecordPull, enabled bool) *RecoveryBackfill {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecoveryBackfill{a: a, record: record, enabled: enabled, ctx: ctx, cancel: cancel, last: map[string]time.Time{}, busy: map[string]bool{}}
}

// OnAnchor schedules the episode's pull. A chat with a pull already queued or
// running mints no second one, so concurrent arrivals consume one attempt.
func (w *RecoveryBackfill) OnAnchor(e domain.RecoveryEpisode) {
	if !w.enabled || e.AnchorID == "" {
		return
	}
	chat, err := domain.Parse(e.ChatJID)
	if err != nil {
		// S1b review F8: never drop an anchored episode silently; the episode
		// keeps it pending and stops re-firing. The JID itself is never logged.
		n := w.invalid.Add(1)
		slog.Default().Warn("recovery backfill: unparsable chat; episode left pending", "generation", e.Generation, "invalid_chats_total", n)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil || w.busy[e.ChatJID] {
		return
	}
	if err != nil {
		w.wg.Go(func() { _ = w.record(w.ctx, e.ChatJID, e.Generation, "invalid_chat", 0) })
		return
	}
	w.busy[e.ChatJID] = true
	wait := time.Until(w.last[e.ChatJID].Add(recoveryCooldown))
	w.wg.Go(func() { w.run(chat, e, wait) })
}

func (w *RecoveryBackfill) run(chat domain.JID, e domain.RecoveryEpisode, wait time.Duration) {
	defer func() {
		w.mu.Lock()
		delete(w.busy, e.ChatJID)
		w.mu.Unlock()
	}()
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-t.C:
		case <-w.ctx.Done():
			return
		}
	}
	release, err := w.a.pulls.acquire(w.ctx, e.ChatJID)
	if err != nil {
		if errors.Is(err, errPullBudget) {
			_ = w.record(w.ctx, e.ChatJID, e.Generation, "queue_overflow", 0)
		}
		return
	}
	defer release()
	w.mu.Lock()
	w.last[e.ChatJID] = time.Now()
	w.mu.Unlock()
	if w.a.closed.Load() || w.a.client == nil || !w.a.client.IsConnected() || !w.a.client.IsLoggedIn() {
		_ = w.record(w.ctx, e.ChatJID, e.Generation, "disconnected", 0)
		return
	}
	anchor := anchorFromRef(domain.MessageRef{Chat: chat, ID: domain.MessageID(e.AnchorID), FromMe: e.AnchorFromMe, Timestamp: time.Unix(e.AnchorTS, 0)})
	res, err := w.a.pullBefore(w.ctx, chat, anchor, historyRoundTripCap, ForceSyncResult{})
	if err != nil || w.ctx.Err() != nil || w.a.clientCtx.Err() != nil {
		return // shutdown: the episode stays pending, nothing measured
	}
	result := "failed"
	switch {
	case res.Delivered:
		result = "delivered"
	case res.TimedOut:
		result = "timed_out"
	}
	_ = w.record(w.ctx, e.ChatJID, e.Generation, result, res.Received)
}

// Close cancels queued and running pulls and waits for them to exit.
func (w *RecoveryBackfill) Close() {
	w.cancel()
	w.wg.Wait()
}
