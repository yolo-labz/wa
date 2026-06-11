package sqlitehistory_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// historyHarness adapts *sqlitehistory.Store to
// porttest.HistoryStoreHarness. AppendHistory stamps an explicit,
// strictly increasing timestamp per call via InsertRaw so HS1's
// newest-first ordering assertion is deterministic regardless of the
// wall clock's resolution.
type historyHarness struct {
	*sqlitehistory.Store
	t  *testing.T
	ts atomic.Int64
}

func (h *historyHarness) AppendHistory(chat domain.JID, msg domain.Message) {
	h.t.Helper()
	body := ""
	if tm, ok := msg.(domain.TextMessage); ok {
		body = tm.Body
	}
	ts := h.ts.Add(1)
	err := h.InsertRaw(context.Background(),
		chat.String(), chat.String(),
		"contract-"+chat.String()+"-"+itoa(ts),
		ts, body, "", "", "", false, nil, "", "")
	if err != nil {
		h.t.Fatalf("AppendHistory via InsertRaw: %v", err)
	}
}

func (h *historyHarness) SupportsRemoteBackfill() bool { return false }

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestHistoryStoreContract certifies the SQLite-backed store against
// the shared HS1–HS6 port contract (018 audit TEST-03: 009 landed
// without porttest coverage; only the memory + whatsmeow composites
// ran these clauses via RunContractSuite).
func TestHistoryStoreContract(t *testing.T) {
	t.Parallel()
	porttest.RunHistoryStoreContract(t, func(t *testing.T) porttest.HistoryStoreHarness {
		dbPath := filepath.Join(t.TempDir(), "messages.db")
		s, err := sqlitehistory.Open(context.Background(), dbPath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return &historyHarness{Store: s, t: t}
	})
}
