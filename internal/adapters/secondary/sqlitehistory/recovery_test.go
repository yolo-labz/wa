package sqlitehistory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	recoveryChatA = "1@hosted"
	recoveryChatB = "2@hosted"
)

func recoveryRecord(chat, id string) domain.HistoryRecord {
	return domain.HistoryRecord{ChatJID: chat, SenderJID: chat, MessageID: id, Timestamp: 1, Body: "local test fixture"}
}

func liveOK(t *testing.T, s *Store, chat, id string) {
	t.Helper()
	if err := s.InsertLive(t.Context(), recoveryRecord(chat, id), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func liveFull(t *testing.T, s *Store, f *faultSQLite, chat, id string) {
	t.Helper()
	f.setCode(13)
	defer f.setCode(0)
	if err := s.InsertLive(t.Context(), recoveryRecord(chat, id), time.Now()); err == nil {
		t.Fatal("wanted SQLITE_FULL")
	}
}

func episode(s *Store, chat string) (domain.RecoveryEpisode, bool) {
	for _, e := range s.RecoveryEpisodes() {
		if e.ChatJID == chat {
			return e, true
		}
	}
	return domain.RecoveryEpisode{}, false
}

func requireEpisode(t *testing.T, s *Store, chat, anchor string, missing int) domain.RecoveryEpisode {
	t.Helper()
	e, ok := episode(s, chat)
	if !ok || e.AnchorID != anchor || e.Missing != missing || e.OutageAt.IsZero() && !e.Unknown {
		t.Fatalf("episode %s = %+v (pending=%v); want anchor %q, %d missing", chat, e, ok, anchor, missing)
	}
	return e
}

func requireNoEpisode(t *testing.T, s *Store, chat string) {
	t.Helper()
	if e, ok := episode(s, chat); ok {
		t.Fatalf("unaffected chat armed: %+v", e)
	}
}

func TestDeferredBackfill_StorageRecoveryArmsUntilFirstLivePersist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.probeStorage(t.Context())
		liveOK(t, s, recoveryChatA, "before")
		requireNoEpisode(t, s, recoveryChatA)
		liveFull(t, s, f, recoveryChatA, "lost")
		requireStorage(t, s, "failed")
		armed := requireEpisode(t, s, recoveryChatA, "", 1)
		requireNoEpisode(t, s, recoveryChatB)

		s.probeStorage(t.Context()) // writability recovers with no live message yet
		requireStorage(t, s, "ok")
		<-time.After(time.Minute)
		requireEpisode(t, s, recoveryChatA, "", 1)
		if err := s.InsertRaw(t.Context(), recoveryRecord(recoveryChatA, "imported")); err != nil {
			t.Fatal(err)
		}
		requireEpisode(t, s, recoveryChatA, "", 1)

		liveOK(t, s, recoveryChatA, "live")
		if e := requireEpisode(t, s, recoveryChatA, "live", 1); e.Generation != armed.Generation {
			t.Fatal("anchor opened a new episode instead of consuming the armed one")
		}
		if h := s.StorageHealth(); h.RecoveryPendingChats != 1 || h.RecoveryState != "tracked" {
			t.Fatalf("pending gap not visible: %+v", h)
		}

		// The commit that clears the latch is itself the anchor: no second message needed.
		liveFull(t, s, f, recoveryChatB, "lost-b")
		requireStorage(t, s, "failed")
		liveOK(t, s, recoveryChatB, "first-b")
		requireStorage(t, s, "ok")
		requireEpisode(t, s, recoveryChatB, "first-b", 1)
	})
}

func TestDeferredBackfill_RestartWithUnpersistedReceiptsArms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "messages.db")
		s, f := openFaultStore(t, path)
		s.probeStorage(t.Context())
		liveOK(t, s, recoveryChatA, "old")
		liveFull(t, s, f, recoveryChatA, "lost")
		<-time.After(time.Second)
		liveOK(t, s, recoveryChatB, "newer") // a newer commit elsewhere must not hide the old receipt
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s.lock = nil

		restarted, _ := openFaultStore(t, path)
		if h := restarted.StorageHealth(); h.RecoveryState != "tracked" || h.RecoveryPendingChats != 1 {
			t.Fatalf("clean restart lost or inflated evidence: %+v", h)
		}
		if e := requireEpisode(t, restarted, recoveryChatA, "", 1); e.Unknown {
			t.Fatal("clean journal restored as unknown")
		}
		requireNoEpisode(t, restarted, recoveryChatB)
		restarted.probeStorage(t.Context())
		liveOK(t, restarted, recoveryChatA, "after-restart")
		requireEpisode(t, restarted, recoveryChatA, "after-restart", 1)
	})
}

func TestDeferredBackfill_UnknownRestartFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		journal func(path string) error
	}{
		{"missing", os.Remove},
		{"corrupt", func(p string) error { return os.WriteFile(p, []byte("{broken"), 0o600) }},
		{"unclean", func(p string) error { return os.WriteFile(p, []byte(`{"version":1,"clean":false,"chats":{}}`), 0o600) }},
		{"wrong_version", func(p string) error { return os.WriteFile(p, []byte(`{"version":2,"clean":true,"chats":{}}`), 0o600) }},
		{"invalid_shape", func(p string) error {
			return os.WriteFile(p, []byte(`{"version":1,"clean":true,"chats":{"`+recoveryChatA+`":null}}`), 0o600)
		}},
		{"unreadable", func(p string) error { return errors.Join(os.Remove(p), os.Mkdir(p, 0o700)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "messages.db")
				s, f := openFaultStore(t, path)
				s.probeStorage(t.Context())
				liveOK(t, s, recoveryChatA, "a")
				liveOK(t, s, recoveryChatB, "b")
				liveOK(t, s, "status@broadcast", "status") // not a conversation: never armed
				liveFull(t, s, f, recoveryChatA, "lost")
				liveOK(t, s, recoveryChatA, "anchor")
				requireEpisode(t, s, recoveryChatA, "anchor", 1)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s.lock = nil
				if err := tc.journal(path + ".recovery"); err != nil {
					t.Fatal(err)
				}
				restarted, _ := openFaultStore(t, path)
				if h := restarted.StorageHealth(); h.RecoveryState != "recovery_unknown" {
					t.Fatalf("missing evidence claimed tracked recovery: %+v", h)
				}
				for _, chat := range []string{recoveryChatA, recoveryChatB} {
					if e, ok := episode(restarted, chat); !ok || !e.Unknown || e.AnchorID != "" {
						t.Fatalf("%s not conservatively armed without an anchor: %+v", chat, e)
					}
				}
				requireNoEpisode(t, restarted, "status@broadcast")
			})
		})
	}
	t.Run("journal_write_fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "messages.db")
			s, f := openFaultStore(t, path)
			s.probeStorage(t.Context())
			s.recovery.mu.Lock()
			s.recovery.save = func(recoveryState) error { return syscall.ENOSPC }
			s.recovery.mu.Unlock()
			liveFull(t, s, f, recoveryChatA, "lost")
			if h := s.StorageHealth(); h.RecoveryState != "recovery_unknown" {
				t.Fatalf("lost watermark read as tracked: %+v", h)
			}
			liveOK(t, s, recoveryChatA, "stored") // the bookkeeping failure never drops a message
			var n int
			if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages WHERE message_id = 'stored'").Scan(&n); err != nil || n != 1 {
				t.Fatalf("message lost to a journal failure: n=%d err=%v", n, err)
			}
			err := s.Close() // the clean marker cannot land either, and Close says so
			s.lock = nil
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("close hid the journal failure: %v", err)
			}
			restarted, _ := openFaultStore(t, path) // disk still holds the startup clean=false marker
			if h := restarted.StorageHealth(); h.RecoveryState != "recovery_unknown" {
				t.Fatalf("failed journal write became a clean restart: %+v", h)
			}
		})
	})
}

func TestDeferredBackfill_FailedInsertDoesNotTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.probeStorage(t.Context())
		liveFull(t, s, f, recoveryChatA, "lost")
		s.probeStorage(t.Context())
		requireStorage(t, s, "ok")

		liveFull(t, s, f, recoveryChatA, "failed-first") // a failed first live insert
		requireEpisode(t, s, recoveryChatA, "", 2)
		s.probeStorage(t.Context())
		if err := s.InsertRaw(t.Context(), recoveryRecord(recoveryChatA, "dup")); err != nil {
			t.Fatal(err)
		}
		liveOK(t, s, recoveryChatA, "dup") // duplicate/no-op live insert
		if err := s.InsertRaw(t.Context(), recoveryRecord(recoveryChatA, "replayed")); err != nil {
			t.Fatal(err)
		}
		liveOK(t, s, recoveryChatB, "unrelated")
		requireEpisode(t, s, recoveryChatA, "", 2)
		requireNoEpisode(t, s, recoveryChatB)

		liveOK(t, s, recoveryChatA, "first-real")
		requireEpisode(t, s, recoveryChatA, "first-real", 2)
	})
}

func TestDeferredBackfill_FirstLiveAnchorExactlyOnce(t *testing.T) {
	s, f := openFaultStore(t, "")
	s.probeStorage(t.Context())
	liveFull(t, s, f, recoveryChatA, "lost")
	s.probeStorage(t.Context())
	armed := requireEpisode(t, s, recoveryChatA, "", 1)

	var wg sync.WaitGroup
	ids := map[string]bool{}
	for i := range 8 {
		id := fmt.Sprintf("concurrent-%d", i)
		ids[id] = true
		wg.Go(func() {
			if err := s.InsertLive(t.Context(), recoveryRecord(recoveryChatA, id), time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	e, _ := episode(s, recoveryChatA)
	if !ids[e.AnchorID] || e.Generation != armed.Generation {
		t.Fatalf("concurrent arrivals did not consume one episode once: %+v", e)
	}
	liveOK(t, s, recoveryChatA, "later")
	if again, _ := episode(s, recoveryChatA); again.AnchorID != e.AnchorID || again.Generation != e.Generation {
		t.Fatalf("a later arrival re-anchored the episode: %+v", again)
	}

	// A write that began before a newer same-chat failure cannot anchor it:
	// the lost message may be newer than the anchor.
	r := s.recovery
	slow := domain.IngestReceipt{ChatJID: recoveryChatB, MessageID: "slow", ReceivedAt: time.Now()}
	lost := domain.IngestReceipt{ChatJID: recoveryChatB, MessageID: "lost-b", ReceivedAt: time.Now()}
	before := r.beginLive(slow)
	r.endLive(lost, StoredMessage{MessageID: "lost-b"}, r.beginLive(lost), syscall.EIO, false)
	r.endLive(slow, StoredMessage{MessageID: "slow"}, before, nil, true)
	requireEpisode(t, s, recoveryChatB, "", 1)
}

// Only a measured pull that leaves every lost receipt stored clears an
// episode; a delivered reply, a stale generation or unknown evidence never do.
func TestRecordRecoveryPull_ClearsOnlyOnResolvedReceipts(t *testing.T) {
	s, f := openFaultStore(t, "")
	s.probeStorage(t.Context())
	liveFull(t, s, f, recoveryChatA, "lost")
	s.probeStorage(t.Context())
	liveOK(t, s, recoveryChatA, "live")
	armed := requireEpisode(t, s, recoveryChatA, "live", 1)

	if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, armed.Generation, "delivered", 50); err != nil {
		t.Fatal(err)
	}
	if e := requireEpisode(t, s, recoveryChatA, "live", 1); e.LastPullResult != "delivered" || e.LastPullReceived != 50 {
		t.Fatalf("measured outcome not recorded: %+v", e)
	}
	if err := s.InsertRaw(t.Context(), recoveryRecord(recoveryChatA, "lost")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, armed.Generation+1, "delivered", 1); err != nil {
		t.Fatal(err)
	}
	requireEpisode(t, s, recoveryChatA, "live", 1) // stale generation: no-op
	if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, armed.Generation, "delivered", 1); err != nil {
		t.Fatal(err)
	}
	requireNoEpisode(t, s, recoveryChatA)
}

func unknownRestart(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	s, _ := openFaultStore(t, path)
	s.probeStorage(t.Context())
	liveOK(t, s, recoveryChatA, "pre-outage")
	liveOK(t, s, recoveryChatB, "pre-outage-b")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s.lock = nil
	if err := os.WriteFile(path+".recovery", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, _ := openFaultStore(t, path)
	restarted.probeStorage(t.Context())
	return restarted
}

func hookLog(s *Store) func() []string {
	var mu sync.Mutex
	var anchors []string
	s.SetRecoveryAnchorHook(func(e domain.RecoveryEpisode) { mu.Lock(); anchors = append(anchors, e.AnchorID); mu.Unlock() })
	return func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(anchors) }
}

// Review F1 ruling: an unknown episode has no receipts to compare, so one
// delivered pull from its own real post-recovery anchor clears it; nothing
// weaker does, and the state leaves recovery_unknown once none remain.
func TestDeferredBackfill_UnknownEpisodeExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := unknownRestart(t)
		a := requireEpisode(t, s, recoveryChatA, "", 0)
		b := requireEpisode(t, s, recoveryChatB, "", 0)
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatB, b.Generation, "delivered", 50); err != nil {
			t.Fatal(err)
		}
		requireEpisode(t, s, recoveryChatB, "", 0) // no own anchor: a pull proves nothing
		liveOK(t, s, recoveryChatA, "anchor-1")
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, a.Generation, "timed_out", 0); err != nil {
			t.Fatal(err)
		}
		requireEpisode(t, s, recoveryChatA, "anchor-1", 0)
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, a.Generation, "delivered", 50); err != nil {
			t.Fatal(err)
		}
		requireNoEpisode(t, s, recoveryChatA)
		if h := s.StorageHealth(); h.RecoveryState != "recovery_unknown" || h.RecoveryPendingChats != 1 {
			t.Fatalf("other unknown chat lost: %+v", h)
		}
		liveOK(t, s, recoveryChatB, "anchor-b")
		b = requireEpisode(t, s, recoveryChatB, "anchor-b", 0)
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatB, b.Generation, "delivered", 3); err != nil {
			t.Fatal(err)
		}
		if h := s.StorageHealth(); h.RecoveryState != "tracked" || h.RecoveryPendingChats != 0 {
			t.Fatalf("unknown state has no exit: %+v", h)
		}
	})
}

// Review F4: receipts resolved by redelivery leave nothing missing, so the
// next measured pull clears the tracked episode.
func TestRecordRecoveryPull_ClearsAfterRedeliveredReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.probeStorage(t.Context())
		liveFull(t, s, f, recoveryChatA, "lost")
		s.probeStorage(t.Context())
		liveOK(t, s, recoveryChatA, "lost") // redelivered and stored
		e := requireEpisode(t, s, recoveryChatA, "lost", 0)
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, e.Generation, "timed_out", 0); err != nil {
			t.Fatal(err)
		}
		requireNoEpisode(t, s, recoveryChatA)
	})
}

// Review F5 ruling: an anchored episode whose pull was not delivered re-fires
// its ORIGINAL anchor on the next qualifying live commit past the cooldown;
// within the cooldown or after a delivered pull it never fires again.
func TestDeferredBackfill_FailedPullRetriesOnNextLiveAfterCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		fired := hookLog(s)
		s.probeStorage(t.Context())
		liveFull(t, s, f, recoveryChatA, "lost")
		s.probeStorage(t.Context())
		liveOK(t, s, recoveryChatA, "live-1")
		e := requireEpisode(t, s, recoveryChatA, "live-1", 1)
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, e.Generation, "timed_out", 0); err != nil {
			t.Fatal(err)
		}
		liveOK(t, s, recoveryChatA, "live-2")
		<-time.After(recoveryRefire)
		liveOK(t, s, recoveryChatA, "live-3")
		liveOK(t, s, recoveryChatA, "live-4")
		if got := fired(); !slices.Equal(got, []string{"live-1", "live-1"}) {
			t.Fatalf("anchor hook fired %v; want the original anchor twice", got)
		}
		if err := s.RecordRecoveryPull(t.Context(), recoveryChatA, e.Generation, "delivered", 50); err != nil {
			t.Fatal(err)
		}
		<-time.After(recoveryRefire)
		liveOK(t, s, recoveryChatA, "live-5")
		requireEpisode(t, s, recoveryChatA, "live-1", 1) // still missing "lost": pending, not re-pulled
		if n := len(fired()); n != 2 {
			t.Fatalf("delivered episode re-fired: %v", fired())
		}
	})
}

func TestDeferredBackfill_RestartRefiresAnchoredEpisodeOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "messages.db")
		s, f := openFaultStore(t, path)
		s.probeStorage(t.Context())
		liveFull(t, s, f, recoveryChatA, "lost")
		s.probeStorage(t.Context())
		liveOK(t, s, recoveryChatA, "live-1")
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s.lock = nil
		restarted, _ := openFaultStore(t, path)
		fired := hookLog(restarted)
		restarted.probeStorage(t.Context())
		liveOK(t, restarted, recoveryChatA, "after-1")
		liveOK(t, restarted, recoveryChatA, "after-2")
		if got := fired(); !slices.Equal(got, []string{"live-1"}) {
			t.Fatalf("restart re-fire: %v; want the original anchor once", got)
		}
	})
}

// Review F7: the presence check is bounded; a wedged query leaves the
// episode pending instead of hanging the deferred-pull worker.
func TestRecordRecoveryPull_PresenceCheckHasDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.probeStorage(t.Context())
		liveFull(t, s, f, recoveryChatA, "lost")
		e := requireEpisode(t, s, recoveryChatA, "", 1)
		f.mu.Lock()
		f.blockID, f.entered, f.release = "lost", make(chan struct{}, 1), make(chan struct{})
		f.mu.Unlock()
		start := time.Now()
		err := s.RecordRecoveryPull(context.Background(), recoveryChatA, e.Generation, "delivered", 1)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 5*time.Second {
			t.Fatalf("presence check unbounded: err=%v after %s", err, time.Since(start))
		}
		requireEpisode(t, s, recoveryChatA, "", 1)
	})
}
