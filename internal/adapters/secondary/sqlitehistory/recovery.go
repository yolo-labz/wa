package sqlitehistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// FR-116-24 recovery markers live in a JSON journal beside messages.db, not in
// SQLite, because they must record a failed write exactly when SQLite cannot
// write. The journal is rewritten only when the evidence changes (a failed live
// insert, an anchor, a resolved receipt, startup and shutdown), never once per
// message. Startup writes clean=false first, so a crash with an insert in flight
// restarts as recovery_unknown and arms every known chat; a journal write
// failure never fails the message insert itself.

type recoveryChat struct {
	domain.RecoveryEpisode
	Receipts map[string]time.Time `json:"receipts"`
	failures uint64               // failed live inserts in this process; orders anchors after them
	fired    time.Time            // last anchor hook in this process; a restart re-fires once
}

// recoveryRefire matches the deferred-pull cooldown: an anchored episode whose
// pull was not delivered re-fires its original anchor on the next qualifying
// live commit past this window (review F5) — never in a loop.
const recoveryRefire = 5 * time.Minute

// S1b review F6: a chat tracks at most maxRecoveryReceipts lost receipts
// (overflow marks it unknown instead), and while the journal is stale a write
// is retried at most once per recoveryRetry rather than on every live insert.
const (
	maxRecoveryReceipts = 256
	recoveryRetry       = 30 * time.Second
)

type recoveryState struct {
	Version int                      `json:"version"`
	Clean   bool                     `json:"clean"`
	Chats   map[string]*recoveryChat `json:"chats"`
}

type recoverySnapshot struct {
	episodes []domain.RecoveryEpisode // pending only
	unknown  bool
	anchored int
}

type recoveryJournal struct {
	mu       sync.Mutex // guards state; health readers use only the snapshot
	state    recoveryState
	inflight int
	stale    bool // the last journal write failed: disk evidence may lag memory
	retryAt  time.Time
	blind    bool // known chats could not be enumerated for an unknown restart
	closed   bool // the clean marker is written; a repeated Close must not rewrite it
	save     func(recoveryState) error
	snapshot atomic.Pointer[recoverySnapshot]
	hook     atomic.Pointer[func(domain.RecoveryEpisode)]
}

// restoreRecovery never reads a missing, unparsable or unclean journal as "no
// gap": every known chat is armed as unknown, and old anchors are dropped
// because lost failures may postdate them.
func restoreRecovery(data []byte, readErr error, known func() ([]string, error), save func(recoveryState) error) (*recoveryJournal, error) {
	r := &recoveryJournal{save: save}
	unknown := readErr != nil || json.Unmarshal(data, &r.state) != nil || r.state.Version != 1
	if unknown {
		r.state = recoveryState{}
	}
	unknown = unknown || !r.state.Clean
	arm := r.validate()
	if unknown || len(arm) > 0 {
		chats, err := known()
		r.blind = err != nil
		for _, chat := range append(arm, chats...) {
			c := r.chat(chat)
			c.Unknown = true
			c.arm(time.Time{})
			c.clearAnchor()
		}
	}
	r.state.Version, r.state.Clean = 1, false
	return r, r.flushLocked()
}

// validate drops malformed entries, returning their chats for conservative
// arming.
func (r *recoveryJournal) validate() (dropped []string) {
	if r.state.Chats == nil {
		r.state.Chats = map[string]*recoveryChat{}
	}
	for chat, c := range r.state.Chats {
		if c == nil || c.ChatJID != chat || (c.Pending && c.Generation == 0) {
			delete(r.state.Chats, chat)
			dropped = append(dropped, chat)
			continue
		}
		if c.Receipts == nil {
			c.Receipts = map[string]time.Time{}
		}
	}
	return dropped
}

func (r *recoveryJournal) chat(chat string) *recoveryChat {
	c := r.state.Chats[chat]
	if c == nil {
		c = &recoveryChat{RecoveryEpisode: domain.RecoveryEpisode{ChatJID: chat}, Receipts: map[string]time.Time{}}
		r.state.Chats[chat] = c
	}
	return c
}

// arm opens an episode, or widens the open one back to an earlier outage time.
func (c *recoveryChat) arm(at time.Time) {
	if !c.Pending {
		c.Generation++
		c.Pending, c.OutageAt = true, at
	}
	if !at.IsZero() && (c.OutageAt.IsZero() || at.Before(c.OutageAt)) {
		c.OutageAt = at
	}
}

func (c *recoveryChat) clearAnchor() {
	c.AnchorID, c.AnchorTS, c.AnchorFromMe = "", 0, false
}

func (r *recoveryJournal) flushLocked() error {
	if r.closed {
		return nil
	}
	var err error
	if !r.stale || !time.Now().Before(r.retryAt) {
		err = r.save(r.state) // a skipped retry stays stale, so health keeps recovery_unknown
		r.stale, r.retryAt = err != nil, time.Now().Add(recoveryRetry)
	}
	snap := &recoverySnapshot{unknown: r.stale || r.blind}
	for _, c := range r.state.Chats {
		if c.Pending {
			e := c.RecoveryEpisode
			e.Missing = len(c.Receipts)
			snap.episodes = append(snap.episodes, e)
			snap.unknown = snap.unknown || e.Unknown
			if e.AnchorID != "" {
				snap.anchored++
			}
		}
	}
	r.snapshot.Store(snap)
	return err
}

// beginLive touches memory only; a crash before endLive is covered by the
// startup clean=false marker.
func (r *recoveryJournal) beginLive(receipt domain.IngestReceipt) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight++
	return r.chat(receipt.ChatJID).failures
}

// endLive records the outcome. Only a changed, qualifying (FR-116-21) live
// commit that began after this chat's latest failure may anchor a pending
// episode, and an episode holds one anchor until a newer failure clears it.
// The anchor hook fires outside the lock: once per anchor, plus the F5 re-fire.
func (r *recoveryJournal) endLive(receipt domain.IngestReceipt, msg StoredMessage, failures uint64, err error, qualifying bool) {
	anchored, ep := r.recordLive(receipt, msg, failures, err, qualifying)
	if fn := r.hook.Load(); anchored && fn != nil {
		(*fn)(ep)
	}
}

func (r *recoveryJournal) recordLive(receipt domain.IngestReceipt, msg StoredMessage, failures uint64, err error, qualifying bool) (anchored bool, ep domain.RecoveryEpisode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	c := r.chat(receipt.ChatJID)
	dirty := false
	switch {
	case err != nil:
		if len(c.Receipts) < maxRecoveryReceipts {
			c.Receipts[receipt.MessageID] = receipt.ReceivedAt
		} else if _, kept := c.Receipts[receipt.MessageID]; !kept {
			c.Unknown = true
		}
		c.failures++
		c.arm(receipt.ReceivedAt)
		c.clearAnchor()
		dirty = true
	default:
		if _, missing := c.Receipts[receipt.MessageID]; missing {
			delete(c.Receipts, receipt.MessageID) // the row is present now, written or duplicate
			dirty = true
		}
		if !qualifying || !c.Pending || c.failures != failures {
			break
		}
		if c.AnchorID == "" {
			c.AnchorID, c.AnchorTS, c.AnchorFromMe = msg.MessageID, msg.Timestamp, msg.IsFromMe
			dirty = true
		} else if c.LastPullResult == "delivered" || c.LastPullResult == "invalid_chat" || time.Since(c.fired) < recoveryRefire || time.Since(c.LastPullAt) < recoveryRefire {
			break
		}
		c.fired, anchored = time.Now(), true
	}
	if dirty || r.stale {
		_ = r.flushLocked() // failure is surfaced as recovery_unknown, never as a lost message
	}
	ep = c.RecoveryEpisode
	ep.Missing = len(c.Receipts)
	return anchored, ep
}

// SetRecoveryAnchorHook registers fn for each episode's first real anchor.
func (s *Store) SetRecoveryAnchorHook(fn func(domain.RecoveryEpisode)) {
	if s.recovery != nil {
		s.recovery.hook.Store(&fn)
	}
}

// RecordRecoveryPull stores one measured deferred-pull outcome. Receipts
// resolve only for rows now present, and an episode clears only once every
// receipt is present. A tracked episode needs nothing more; an unknown one has
// no receipts to compare, so it clears after one delivered pull from its own
// real anchor (the strongest evidence left) and says so in the log.
func (s *Store) RecordRecoveryPull(ctx context.Context, chat string, gen uint64, result string, received int) error {
	r := s.recovery
	if r == nil {
		return nil
	}
	r.mu.Lock()
	c := r.state.Chats[chat]
	if c == nil || !c.Pending || c.Generation != gen {
		r.mu.Unlock()
		return nil
	}
	ids := slices.Collect(maps.Keys(c.Receipts))
	r.mu.Unlock()
	present, err := s.presentIDs(ctx, chat, ids)
	r.mu.Lock()
	defer r.mu.Unlock()
	if c = r.state.Chats[chat]; c == nil || !c.Pending || c.Generation != gen {
		return err
	}
	c.LastPullAt, c.LastPullResult, c.LastPullReceived = time.Now().UTC(), result, received
	for _, id := range present {
		delete(c.Receipts, id)
	}
	if err == nil && len(c.Receipts) == 0 && (!c.Unknown || result == "delivered" && c.AnchorID != "") {
		if c.Unknown {
			slog.Default().Warn("sqlitehistory: recovery cleared unverified", "generation", gen, "received", received)
		}
		c.Pending, c.Unknown = false, false
		c.clearAnchor()
	}
	return errors.Join(err, r.flushLocked())
}

func (s *Store) presentIDs(ctx context.Context, chat string, ids []string) (present []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, id := range ids {
		var one int
		switch err = s.db.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE chat_jid = ? AND message_id = ?", chat, id).Scan(&one); {
		case err == nil:
			present = append(present, id)
		case !errors.Is(err, sql.ErrNoRows):
			return present, err
		}
	}
	return present, nil
}

func (s *Store) openRecovery(ctx context.Context) {
	path := filepath.Clean(s.dbPath + ".recovery")
	data, readErr := os.ReadFile(path)
	known := func() ([]string, error) { return s.conversationChats(ctx) }
	var err error
	s.recovery, err = restoreRecovery(data, readErr, known, func(st recoveryState) error { return saveRecovery(path, st) })
	if err != nil {
		// The clean=false marker did not land; never let an old clean marker vouch for this run.
		_ = os.Remove(path)
	}
}

func (s *Store) conversationChats(ctx context.Context) (chats []string, err error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT chat_jid FROM messages")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var chat string
		if err = rows.Scan(&chat); err != nil {
			return nil, err
		}
		if !domain.IsNonConversationChat(chat) {
			chats = append(chats, chat)
		}
	}
	return chats, rows.Err()
}

// saveRecovery replaces the journal atomically, so a reader sees the old or
// the new evidence, never half.
func saveRecovery(path string, state recoveryState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, nil)
}

// RecoveryEpisodes returns the pending episodes from cached state; it never
// waits on SQLite or the journal. Reachability is decided per episode.
func (s *Store) RecoveryEpisodes() []domain.RecoveryEpisode {
	if s.recovery == nil {
		return nil
	}
	return slices.Clone(s.recovery.snapshot.Load().episodes)
}

func (s *Store) recoveryView(v *domain.StorageHealth) {
	if s.recovery == nil {
		return
	}
	snap := s.recovery.snapshot.Load()
	v.RecoveryState, v.RecoveryPendingChats, v.RecoveryAnchoredChats = "tracked", len(snap.episodes), snap.anchored
	if snap.unknown {
		v.RecoveryState = "recovery_unknown"
	}
}

// closeRecovery marks the journal clean only when no live insert is in flight.
func (s *Store) closeRecovery() error {
	if s.recovery == nil {
		return nil
	}
	r := s.recovery
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Clean = r.inflight == 0
	r.retryAt = time.Time{} // the final write is never rate-limited
	err := r.flushLocked()
	r.closed = true
	return err
}
