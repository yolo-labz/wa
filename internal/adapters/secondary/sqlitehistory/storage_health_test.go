package sqlitehistory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

type storageConnected struct{}

func (storageConnected) WebsocketConnected() bool { return true }
func storageDispatcher(t *testing.T, s *Store) *app.Dispatcher {
	t.Helper()
	m := memory.New(nil)
	session, err := domain.NewSession(domain.MustJID("15551234567@s.whatsapp.net"), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Save(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: storageConnected{}, StorageHealth: s.StorageHealth})
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func liveRecord(id string) domain.HistoryRecord {
	return domain.HistoryRecord{ChatJID: "15551234567@s.whatsapp.net", SenderJID: "15557654321@s.whatsapp.net", MessageID: id, Timestamp: 1, Body: "local test fixture"}
}

func insertLive(s *Store, id string) error {
	return s.InsertLive(context.Background(), liveRecord(id), time.Now())
}

func requireStorage(t *testing.T, s *Store, want string) {
	t.Helper()
	if got := s.StorageHealth(); got.Storage != want {
		t.Fatalf("storage=%+v; want %s", got, want)
	}
}

func requireReady(t *testing.T, d *app.Dispatcher, want bool) {
	t.Helper()
	if d.IsReady(t.Context()) != want {
		t.Fatalf("ready=%v reasons=%v", !want, d.ReadinessReasons(t.Context()))
	}
	for _, method := range []string{"health", "status"} {
		raw, err := d.Handle(t.Context(), method, nil)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Health  string
			Reasons []string
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if !want && value.Health == "healthy" {
			t.Fatalf("%s lies: %s", method, raw)
		}
	}
}

func TestStorageProbe_StartsUnknownAndUnready(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		requireStorage(t, s, "unknown")
		requireReady(t, d, false)
	})
}

func TestStorageProbe_FailureLatchesPastFiveMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		requireReady(t, d, true)
		f.setCode(13)
		if insertLive(s, "full") == nil {
			t.Fatal("expected FULL")
		}
		<-time.After(6 * time.Minute)
		requireStorage(t, s, "failed")
		requireReady(t, d, false)
		if v := s.StorageHealth(); v.PersistFailuresTotal != 1 || v.LastPersistFailureUnixS == 0 {
			t.Fatalf("missing failure evidence: %+v", v)
		}
	})
}

func TestStorageProbe_DatabaseOnlyFailure(t *testing.T) {
	for _, code := range []int{13, 8, 10 | (3 << 8), 14, 11} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := openFaultStore(t, "")
				d := storageDispatcher(t, s)
				s.probeStorage(t.Context())
				f.setCode(code)
				s.probeStorage(t.Context())
				requireStorage(t, s, "failed")
				requireReady(t, d, false)
				if err := os.WriteFile(filepath.Join(filepath.Dir(s.dbPath), "writable"), []byte("fixture"), 0o600); err != nil {
					t.Fatalf("filesystem is not writable: %v", err)
				}
			})
		})
	}
}

func TestStorageProbe_RecoversOnlyAfterLaterWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		old, _ := s.beginPersistence(t.Context(), []StoredMessage{{ChatJID: "fixture", MessageID: "old"}})
		f.setCode(13)
		if insertLive(s, "failed") == nil {
			t.Fatal("expected FULL")
		}
		s.health.finish(old, nil, true, false)
		requireStorage(t, s, "failed")
		f.setCode(0)
		if err := insertLive(s, "later"); err != nil {
			t.Fatal(err)
		}
		requireReady(t, d, true)
		if v := s.StorageHealth(); v.PersistFailuresTotal != 1 || len(s.UnresolvedReceipts()) != 1 {
			t.Fatalf("later write hid receipt failure: %+v", v)
		}
	})
}

func TestStorageProbe_UpsertSucceedsMessageInsertFullStays503(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		f.messageOnly = true
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		f.setCode(13)
		if insertLive(s, "first-full") == nil {
			t.Fatal("expected FULL")
		}
		for i := range 12 {
			// A tiny probe that was already in flight must never clear the latch.
			if err := s.probeTransaction(t.Context(), false, 0); err != nil {
				t.Fatal(err)
			}
			s.health.mu.Lock()
			s.health.seq++
			seq := s.health.seq
			s.health.mu.Unlock()
			s.health.finish(seq, nil, false, true)
			if insertLive(s, fmt.Sprint(i)) == nil {
				t.Fatal("expected FULL again")
			}
			<-time.After(30 * time.Second)
			requireStorage(t, s, "failed")
			requireReady(t, d, false)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.upserts < 13 {
			t.Fatalf("tiny upserts did not run: %d", f.upserts)
		}
	})
}

func TestStorageProbe_StaleProbeCountsAsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.health.interval = 30 * time.Second
		s.probeStorage(t.Context())
		<-time.After(61 * time.Second)
		if err := insertLive(s, "fresh-message"); err != nil {
			t.Fatal(err)
		}
		requireReady(t, d, false)
		if v := s.StorageHealth(); v.Storage != "failed" || v.StorageDetail != "probe_stale" {
			t.Fatalf("stale probe hidden by insert: %+v", v)
		}
	})
}

func TestStorageProbe_BusyAfterTimeoutLatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		f.setCode(5)
		started := time.Now()
		if insertLive(s, "busy") == nil {
			t.Fatal("expected BUSY timeout")
		}
		if elapsed := time.Since(started); elapsed != 5*time.Second {
			t.Fatalf("busy deadline=%v", elapsed)
		}
		requireStorage(t, s, "failed")
		requireReady(t, d, false)
		f.setCode(0)
		f.mu.Lock()
		f.busyRemaining = 2
		f.mu.Unlock()
		if err := insertLive(s, "retry"); err != nil {
			t.Fatal(err)
		}
		requireReady(t, d, true)
		if s.StorageHealth().PersistFailuresTotal != 1 {
			t.Fatal("transient busy counted as failure")
		}
	})
}

func TestIngestLag_NewerInsertDoesNotHideOlderFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		oldReceipt := domain.IngestReceipt{ChatJID: liveRecord("old").ChatJID, MessageID: "old", ReceivedAt: time.Now()}
		old, err := s.beginPersistence(context.WithValue(t.Context(), liveReceiptKey{}, oldReceipt), []StoredMessage{{ChatJID: oldReceipt.ChatJID, MessageID: "old"}})
		if err != nil {
			t.Fatal(err)
		}
		<-time.After(301 * time.Second)
		if err = insertLive(s, "new"); err != nil {
			t.Fatal(err)
		}
		if lag := s.StorageHealth().IngestLagS; lag != 301 {
			t.Fatalf("newer insert hid oldest pending: %d", lag)
		}
		requireReady(t, d, false)
		s.health.finish(old, faultCode(13), false, false)
		requireStorage(t, s, "failed")
		if lag := s.StorageHealth().IngestLagS; lag != 0 {
			t.Fatalf("failure remained pending: %d", lag)
		}
		f.setCode(0)
		if err = insertLive(s, "even-newer"); err != nil {
			t.Fatal(err)
		}
		requireReady(t, d, true)
		failures := s.UnresolvedReceipts()
		if len(failures) != 1 || failures[0].MessageID != "old" {
			t.Fatalf("failure hidden: %+v", failures)
		}
		failures[0].MessageID = "mutated-copy"
		if s.UnresolvedReceipts()[0].MessageID != "old" {
			t.Fatal("receipt seam leaked mutable state")
		}
	})
}

func TestIngestLag_IdleAccountStaysReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.StartStorageProbe(t.Context(), 30*time.Second)
		synctest.Wait()
		<-time.After(20 * time.Minute)
		synctest.Wait()
		requireReady(t, d, true)
		if v := s.StorageHealth(); v.IngestLagS != 0 || v.LastReceivedUnixS != 0 || v.LastPersistedUnixS != 0 {
			t.Fatalf("idle account fabricated receipts: %+v", v)
		}
	})
}

func TestStorageProbe_IdleRestartBecomesReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		f.setCode(13)
		record := liveRecord("large")
		record.RawProto = make([]byte, 160*1024)
		if err := s.InsertLive(t.Context(), record, time.Now()); err == nil {
			t.Fatal("expected FULL")
		}
		path := s.dbPath
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s.lock = nil
		restarted, peer := openFaultStore(t, path)
		d := storageDispatcher(t, restarted)
		requireStorage(t, restarted, "unknown")
		requireReady(t, d, false)
		restarted.StartStorageProbe(t.Context(), 30*time.Second)
		synctest.Wait()
		requireReady(t, d, true)
		if restarted.StorageHealth().LastReceivedUnixS != 0 {
			t.Fatal("recovery invented live receipt")
		}
		peer.mu.Lock()
		defer peer.mu.Unlock()
		if len(peer.fixtureSizes) != 1 || peer.fixtureSizes[0] < 160*1024 {
			t.Fatalf("lost bound on restart: %v", peer.fixtureSizes)
		}
	})
}

func TestStorageHighWater_MergesBeforeFirstLiveInsert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := openFaultStore(t, "")
		if err := os.WriteFile(s.dbPath+".health-size", []byte("00000000000000200000\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := insertLive(s, "small"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(s.dbPath + ".health-size")
		if err != nil {
			t.Fatal(err)
		}
		size, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || size < 200000 {
			t.Fatalf("restart shrank durable bound: %q %v", b, err)
		}
	})
}

func TestStorageProbe_DuplicateDoesNotClearLatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		if err := insertLive(s, "duplicate"); err != nil {
			t.Fatal(err)
		}
		f.setCode(13)
		if insertLive(s, "lost") == nil {
			t.Fatal("expected FULL")
		}
		f.setCode(0)
		<-time.After(time.Second)
		if err := insertLive(s, "duplicate"); err != nil {
			t.Fatal(err)
		}
		requireStorage(t, s, "failed")
		requireReady(t, d, false)
		if v := s.StorageHealth(); v.IngestLagS != 0 || v.LastPersistedUnixS != time.Now().Unix() {
			t.Fatalf("duplicate did not count as persisted: %+v", v)
		}
	})
}

func TestStorageProbe_ConstraintDoesNotLatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		s.probeStorage(t.Context())
		f.setCode(19)
		if insertLive(s, "constraint") == nil {
			t.Fatal("expected constraint")
		}
		requireReady(t, d, true)
		if v := s.StorageHealth(); v.PersistLogicErrorsTotal != 1 || v.PersistFailuresTotal != 0 || len(s.UnresolvedReceipts()) != 1 {
			t.Fatalf("misclassified constraint: %+v", v)
		}
		f.setCode(13)
		if insertLive(s, "full") == nil {
			t.Fatal("expected FULL")
		}
		f.setCode(19)
		s.probeStorage(t.Context())
		requireStorage(t, s, "failed")
		requireReady(t, d, false)
	})
}

func TestStorageProbe_RecoveryFixtureFloorAndNoEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		d := storageDispatcher(t, s)
		events, cancel := d.Bridge().SubscribeStream(nil, 8)
		defer cancel()
		s.probeStorage(t.Context())
		synctest.Wait()
		f.mu.Lock()
		sizes := append([]int(nil), f.fixtureSizes...)
		f.mu.Unlock()
		if len(sizes) != 1 || sizes[0] < 65536 {
			t.Fatalf("undersized fixture: %v", sizes)
		}
		var rows int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM messages WHERE chat_jid='wad-health@local'").Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("fixture leaked: %d %v", rows, err)
		}
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM messages_fts_docsize").Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("FTS fixture leaked: %d %v", rows, err)
		}
		if v := s.StorageHealth(); v.LastReceivedUnixS != 0 || v.LastPersistedUnixS != 0 {
			t.Fatalf("fixture advanced watermarks: %+v", v)
		}
		select {
		case e := <-events:
			t.Fatalf("fixture emitted event/webhook input: %+v", e)
		default:
		}
		// A real trigger rejection must fail the recovery path, not just the tiny probe.
		if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER reject_probe BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'fixture rejected'); END"); err != nil {
			t.Fatal(err)
		}
		s.health.mu.Lock()
		s.health.view.Storage = "failed"
		s.health.mu.Unlock()
		s.probeStorage(t.Context())
		requireReady(t, d, false)
	})
}

func TestStorageProbe_HistoryImportsDoNotAdvanceLiveClocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := openFaultStore(t, "")
		s.probeStorage(t.Context())
		if err := s.InsertRawInteractive(t.Context(), liveRecord("history")); err != nil {
			t.Fatal(err)
		}
		if v := s.StorageHealth(); v.LastReceivedUnixS != 0 || v.LastPersistedUnixS != 0 {
			t.Fatalf("import changed live clocks: %+v", v)
		}
		if err := insertLive(s, "live"); err != nil {
			t.Fatal(err)
		}
		v := s.StorageHealth()
		if v.LastReceivedUnixS != time.Now().Unix() || v.LastPersistedUnixS != time.Now().Unix() {
			t.Fatalf("used sender timestamp: %+v", v)
		}
	})
}

func TestStorageProbe_ShutdownAndCancelledBusyDoNotLatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.StartStorageProbe(t.Context(), 30*time.Second)
		synctest.Wait()
		f.setCode(5)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := s.InsertLive(ctx, liveRecord("cancelled"), time.Now()); err == nil {
			t.Fatal("expected cancellation")
		}
		requireStorage(t, s, "ok")
		if s.StorageHealth().PersistFailuresTotal != 0 {
			t.Fatal("short BUSY latched")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s.lock = nil
		select {
		case <-s.health.done:
		default:
			t.Fatal("worker outlived Close")
		}
	})
}

func TestStorageProbe_OlderCommitAndCachedReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := openFaultStore(t, "")
		s.db.SetMaxOpenConns(2)
		s.probeStorage(t.Context())
		d := storageDispatcher(t, s)
		f.blockBegin = true
		f.entered = make(chan struct{}, 1)
		f.release = make(chan struct{})
		old := make(chan error, 1)
		go func() { old <- insertLive(s, "old-inflight") }()
		<-f.entered
		f.setCode(13)
		if insertLive(s, "new-failure") == nil {
			t.Fatal("expected FULL")
		}
		f.setCode(0)
		f.mu.Lock()
		queries := len(f.queries)
		f.mu.Unlock()
		started := time.Now()
		for range 100 {
			requireReady(t, d, false)
		}
		if time.Since(started) >= time.Millisecond {
			t.Fatal("cached reader waited for database")
		}
		f.mu.Lock()
		after := len(f.queries)
		f.mu.Unlock()
		if queries != after {
			t.Fatalf("health reader queried database: %d -> %d", queries, after)
		}
		close(f.release)
		if err := <-old; err != nil {
			t.Fatal(err)
		}
		requireStorage(t, s, "failed")
		requireReady(t, d, false)
		s.probeStorage(t.Context())
		requireReady(t, d, true)
	})
}

func TestMigrateV9_HealthProbeTable(t *testing.T) {
	s, _ := openFaultStore(t, "")
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE health_probe; DELETE FROM migration_history WHERE version=9; PRAGMA user_version=8;"); err != nil {
		t.Fatal(err)
	}
	if err := migrateIfNeeded(t.Context(), s.db, &MigrateOpts{DBPath: s.dbPath, BackupsDir: filepath.Join(t.TempDir(), "backups")}); err != nil {
		t.Fatal(err)
	}
	version, err := schemaVersion(t.Context(), s.db)
	if err != nil || version != 9 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var backup string
	if err = s.db.QueryRowContext(t.Context(), "SELECT backup_path FROM migration_history WHERE version=9 AND direction='up'").Scan(&backup); err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("v9 skipped its backup")
	}
	if _, err = os.Stat(backup); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), "INSERT INTO health_probe(id,wrote_at) VALUES(2,1)"); err == nil {
		t.Fatal("probe singleton constraint missing")
	}
	s.probeStorage(t.Context())
	requireStorage(t, s, "ok")
}
