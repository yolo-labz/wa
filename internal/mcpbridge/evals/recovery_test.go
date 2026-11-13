package evals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const evalChat = "15550000001@s.whatsapp.net"

type recoveryStatus struct {
	Storage  string `json:"storage"`
	State    string `json:"recovery_state"`
	Pending  int    `json:"recovery_pending_chats"`
	Anchored int    `json:"recovery_anchored_chats"`
}

// evalRecoveryStore restarts a real history store over lost recovery markers
// (a corrupt journal), the same evidence gap an unclean crash leaves behind.
func evalRecoveryStore(t *testing.T) *sqlitehistory.Store {
	t.Helper()
	db := filepath.Join(t.TempDir(), "m.db")
	s, err := sqlitehistory.Open(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	pre := domain.HistoryRecord{ChatJID: evalChat, SenderJID: evalChat, MessageID: "pre-outage", Timestamp: 1_700_000_000, Body: "x"}
	if err = s.InsertLive(t.Context(), pre, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(db+".recovery", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = sqlitehistory.Open(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func recoveryStatusOf(t *testing.T, s *sqlitehistory.Store) recoveryStatus {
	t.Helper()
	m := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: statusProbe(true), StorageHealth: s.StorageHealth})
	defer func() { _ = d.Close() }()
	var v recoveryStatus
	if err := json.Unmarshal([]byte(callStatus(t, d)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// f9: writable storage is not gap recovery. The episode stays pending and
// unanchored until a real live message commits, and that commit anchors it
// without clearing it; only a measured backfill result may.
func evalRecoveryDefersUntilLiveAnchor(t *testing.T) {
	s := evalRecoveryStore(t)
	if v := recoveryStatusOf(t, s); v.Pending != 1 || v.Anchored != 0 {
		t.Fatalf("before a live message: %+v", v)
	}
	live := domain.HistoryRecord{ChatJID: evalChat, SenderJID: evalChat, MessageID: "live-1", Timestamp: 1_700_000_500, Body: "y"}
	if err := s.InsertLive(t.Context(), live, time.Now()); err != nil {
		t.Fatal(err)
	}
	if v := recoveryStatusOf(t, s); v.Storage != "ok" || v.Pending != 1 || v.Anchored != 1 {
		t.Fatalf("after the first live commit: %+v", v)
	}
	if eps := s.RecoveryEpisodes(); len(eps) != 1 || eps[0].AnchorID != "live-1" {
		t.Fatalf("anchor is not the live commit: %+v", eps)
	}
}

// f10: a newer pre-outage row never makes the tail gap look reachable; lost
// markers surface as recovery_unknown with nothing anchored.
func evalTailGapIsUnreachable(t *testing.T) {
	s := evalRecoveryStore(t)
	v := recoveryStatusOf(t, s)
	if v.State != "recovery_unknown" || v.Pending != 1 || v.Anchored != 0 {
		t.Fatalf("tail gap reported as reachable or recovered: %+v", v)
	}
	if eps := s.RecoveryEpisodes(); len(eps) != 1 || !eps[0].Unknown || eps[0].AnchorID != "" {
		t.Fatalf("pre-outage row became an anchor: %+v", eps)
	}
}
