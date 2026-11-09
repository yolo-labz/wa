package sqlitehistory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	Receipts    map[string]time.Time `json:"receipts"`
	ReceivedAt  time.Time            `json:"last_received_at"`
	PersistedAt time.Time            `json:"last_persisted_at"`
	failures    uint64               // failed live inserts in this process; orders anchors after them
}

type recoveryState struct {
	Version int                      `json:"version"`
	Clean   bool                     `json:"clean"`
	Chats   map[string]*recoveryChat `json:"chats"`
}

type recoverySnapshot struct {
	episodes []domain.RecoveryEpisode // pending only
	unknown  bool
}

type recoveryJournal struct {
	mu       sync.Mutex // guards state; health readers use only the snapshot
	state    recoveryState
	inflight int
	stale    bool // the last journal write failed: disk evidence may lag memory
	blind    bool // known chats could not be enumerated for an unknown restart
	closed   bool // the clean marker is written; a repeated Close must not rewrite it
	save     func(recoveryState) error
	snapshot atomic.Pointer[recoverySnapshot]
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
// arming, and re-arms any chat whose receipts outran its persisted rows.
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
		if len(c.Receipts) > 0 || c.PersistedAt.Before(c.ReceivedAt) {
			c.arm(c.ReceivedAt)
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
	err := r.save(r.state)
	r.stale = err != nil
	snap := &recoverySnapshot{unknown: r.stale || r.blind}
	for _, c := range r.state.Chats {
		if c.Pending {
			e := c.RecoveryEpisode
			e.Missing = len(c.Receipts)
			snap.episodes = append(snap.episodes, e)
			snap.unknown = snap.unknown || e.Unknown
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
	c := r.chat(receipt.ChatJID)
	if receipt.ReceivedAt.After(c.ReceivedAt) {
		c.ReceivedAt = receipt.ReceivedAt
	}
	r.inflight++
	return c.failures
}

// endLive records the outcome. Only a changed, qualifying (FR-116-21) live
// commit that began after this chat's latest failure may anchor a pending
// episode, and an episode holds one anchor until a newer failure clears it.
func (r *recoveryJournal) endLive(receipt domain.IngestReceipt, msg StoredMessage, failures uint64, err error, qualifying bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	c := r.chat(receipt.ChatJID)
	dirty := false
	switch {
	case err != nil:
		c.Receipts[receipt.MessageID] = receipt.ReceivedAt
		c.failures++
		c.arm(receipt.ReceivedAt)
		c.clearAnchor()
		dirty = true
	default:
		if receipt.ReceivedAt.After(c.PersistedAt) {
			c.PersistedAt = receipt.ReceivedAt
		}
		if _, missing := c.Receipts[receipt.MessageID]; missing {
			delete(c.Receipts, receipt.MessageID) // the row is present now, written or duplicate
			dirty = true
		}
		if qualifying && c.Pending && c.AnchorID == "" && c.failures == failures {
			c.AnchorID, c.AnchorTS, c.AnchorFromMe = msg.MessageID, msg.Timestamp, msg.IsFromMe
			dirty = true
		}
	}
	if dirty || r.stale {
		_ = r.flushLocked() // failure is surfaced as recovery_unknown, never as a lost message
	}
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

// saveRecovery replaces the journal atomically (temp file, fsync, rename,
// directory fsync), so a reader sees the old or the new evidence, never half.
func saveRecovery(path string, state recoveryState) (err error) {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".recovery-*")
	if err != nil {
		return err
	}
	defer func() {
		if e := os.Remove(f.Name()); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// RecoveryEpisodes returns the pending episodes from cached state; it never
// waits on SQLite or the journal.
func (s *Store) RecoveryEpisodes() []domain.RecoveryEpisode {
	if s.recovery == nil {
		return nil
	}
	return append([]domain.RecoveryEpisode(nil), s.recovery.snapshot.Load().episodes...)
}

func (s *Store) recoveryView(v *domain.StorageHealth) {
	if s.recovery == nil {
		return
	}
	snap := s.recovery.snapshot.Load()
	v.RecoveryState, v.RecoveryPendingChats = "tracked", len(snap.episodes)
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
	err := r.flushLocked()
	r.closed = true
	return err
}
