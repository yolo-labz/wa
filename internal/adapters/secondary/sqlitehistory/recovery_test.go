package sqlitehistory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
