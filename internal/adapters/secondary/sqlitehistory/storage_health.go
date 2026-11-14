package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"golang.org/x/sys/unix"
)

// Observations, never database/filesystem operations, hold mu. Write-start
// sequences prevent an older commit from clearing a subsequently observed failure.
type storageHealth struct {
	mu                       sync.Mutex
	journal                  sync.Mutex
	journalFailed            bool                 // guarded by journal; logs one line per failure episode
	journalSync              func(*os.File) error // test seam; nil means (*os.File).Sync
	seq, failed              uint64
	probe, started, received time.Time
	interval                 time.Duration
	pending                  map[uint64]domain.IngestReceipt
	failures                 []domain.IngestReceipt
	failedChats              map[string]bool // chats with a retained failure (review F4)
	dropWarnedAt             time.Time       // last cap Warn; at most one a minute (review L1)
	sizes                    map[uint64]int  // in-flight records/fixtures above the 64 KiB floor
	failedMax                int             // largest failed record no recovery fixture has proven yet
	disk                     int             // cached journal value, guarded by journal (review F6)
	diskRead                 bool            // guarded by journal
	stop                     context.CancelFunc
	done                     chan struct{}
	view                     domain.StorageHealth
}

type liveReceiptKey struct{}

// InsertLive is the inbound handler's receipt boundary; history imports use
// InsertRaw/InsertRawInteractive and cannot advance live receipt watermarks.
func (s *Store) InsertLive(ctx context.Context, rec domain.HistoryRecord, received time.Time) error {
	receipt := domain.IngestReceipt{ChatJID: rec.ChatJID, MessageID: rec.MessageID, ReceivedAt: received}
	return s.InsertRawInteractive(context.WithValue(ctx, liveReceiptKey{}, receipt), rec)
}

func (s *Store) beginPersistence(ctx context.Context, msgs []StoredMessage) (uint64, error) {
	h := &s.health
	h.mu.Lock()
	h.seq++
	seq := h.seq
	if r, ok := ctx.Value(liveReceiptKey{}).(domain.IngestReceipt); ok {
		if h.pending == nil {
			h.pending = make(map[uint64]domain.IngestReceipt)
		}
		h.pending[seq] = r
		if r.ReceivedAt.After(h.received) {
			h.received = r.ReceivedAt
			h.view.LastReceivedUnixS, h.view.LastReceivedAt = r.ReceivedAt.Unix(), r.ReceivedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	size := 0
	for _, m := range msgs {
		size = max(size, len(m.RawProto)+len(m.Body)+len(m.Caption)+len(m.InteractiveJSON)+len(m.ChatJID)+len(m.SenderJID)+len(m.MessageID)+len(m.PushName)+len(m.SenderAltJID)+len(m.AddressingMode)+len(m.MediaType)+1024)
	}
	h.trackSizeLocked(seq, size)
	h.mu.Unlock()
	s.syncHighWater()
	return seq, nil
}

func (h *storageHealth) trackSizeLocked(seq uint64, size int) {
	if size <= 65536 {
		return
	}
	if h.sizes == nil {
		h.sizes = make(map[uint64]int)
	}
	h.sizes[seq] = size
}

// syncHighWater makes the journal hold the current fixture bound — the largest
// in-flight record and the largest failed record not yet proven by a recovery
// fixture — BEFORE the corresponding insert, and returns it. The file is read
// once and rewritten only when the bound changes (review F5/F6), so one big
// message no longer ratchets every later recovery. The journal is only a lower
// bound, never a safety invariant: an unreadable/short file falls back to the
// 64 KiB floor (FR-116-21 review F1), and a failed write keeps the in-memory
// bound so the insert itself decides — a full disk then latches storage via
// SQLITE_FULL, not a logic error.
func (s *Store) syncHighWater() int {
	h := &s.health
	h.journal.Lock()
	defer h.journal.Unlock()
	path := s.dbPath + ".health-size"
	if !h.diskRead {
		h.diskRead = true
		b, err := os.ReadFile(filepath.Clean(path))
		if err == nil {
			h.disk, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil || h.disk < 65536 {
				h.disk, err = 0, fmt.Errorf("invalid content %q", b)
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Default().Warn("sqlitehistory: health size journal unreadable; using 64 KiB floor", "err", err)
		}
		h.mu.Lock()
		h.failedMax = max(h.failedMax, h.disk) // a restored bound is unproven until a fixture commits
		h.mu.Unlock()
	}
	h.mu.Lock()
	size := max(h.failedMax, 65536)
	for _, n := range h.sizes {
		size = max(size, n)
	}
	h.mu.Unlock()
	if size == h.disk { // a missing or torn file (disk=0) is rewritten once
		return size
	}
	text := strconv.Itoa(size)
	err := writeFileAtomic(path, []byte(strings.Repeat("0", 20-len(text))+text+"\n"), h.journalSync)
	if err != nil && !h.journalFailed {
		slog.Default().Warn("sqlitehistory: health size journal write failed; keeping in-memory bound", "err", err)
	}
	if h.journalFailed = err != nil; err == nil {
		h.disk = size
	}
	return size
}

// writeFileAtomic replaces path via temp file, fsync, rename and directory
// fsync, so a crash or ENOSPC leaves either the old file or the new one.
func writeFileAtomic(path string, data []byte, sync func(*os.File) error) (err error) {
	if sync == nil {
		sync = (*os.File).Sync
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = sync(f)
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

func sqliteCode(err error) int {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code() & 255
	}
	return 0
}

type busyTimeout struct{ error }

func storageFailure(err error) bool {
	var busy busyTimeout
	if errors.As(err, &busy) {
		return true
	}
	switch sqliteCode(err) {
	case 8, 10, 11, 13, 14:
		return true
	}
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EIO)
}

// finish reports whether this was a qualifying write: a committed change that
// began after the latest storage failure (FR-116-21).
func (h *storageHealth) finish(seq uint64, err error, changed, probe bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	receipt, live := h.pending[seq]
	delete(h.pending, seq)
	size := h.sizes[seq]
	delete(h.sizes, seq)
	if err != nil {
		if live {
			h.retainFailureLocked(receipt, now)
		}
		if !probe {
			h.failedMax = max(h.failedMax, size)
		}
		if storageFailure(err) {
			h.seq++
			h.failed = h.seq
			h.view.Storage = "failed"
			h.view.PersistFailuresTotal++
			h.view.LastPersistFailureUnixS, h.view.LastPersistFailureAt = now.Unix(), now.UTC().Format(time.RFC3339Nano)
		} else {
			h.view.PersistLogicErrorsTotal++
		}
		return false
	}
	if live {
		h.view.LastPersistedUnixS, h.view.LastPersistedAt = now.Unix(), now.UTC().Format(time.RFC3339Nano)
	}
	if probe {
		h.probe = now
		if changed && size >= h.failedMax {
			h.failedMax = 0 // this recovery fixture proved the failed size writable
		}
	}
	if changed && seq > h.failed {
		h.view.Storage = "ok"
		return true
	}
	return false
}

// maxUnresolved caps the in-memory failure list (review F4). Past it, each
// chat keeps its oldest unresolved receipt, the one a newer commit must not
// hide, and later receipts for already-listed chats are counted, not kept.
const maxUnresolved = 1024

func (h *storageHealth) retainFailureLocked(r domain.IngestReceipt, now time.Time) {
	if h.failedChats == nil {
		h.failedChats = make(map[string]bool)
	}
	if len(h.failures) >= maxUnresolved && h.failedChats[r.ChatJID] {
		h.view.FailuresDroppedTotal++
		if now.Sub(h.dropWarnedAt) >= time.Minute {
			h.dropWarnedAt = now
			slog.Default().Warn("sqlitehistory: unresolved receipts capped; keeping the oldest per chat",
				"cap", maxUnresolved, "failures_dropped_total", h.view.FailuresDroppedTotal)
		}
		return
	}
	h.failedChats[r.ChatJID] = true
	h.failures = append(h.failures, r)
}

// UnresolvedReceipts returns the receipt identities still unresolved. S1b
// consumes this typed seam for durable per-chat recovery bookkeeping;
// writability recovery never deletes them.
func (s *Store) UnresolvedReceipts() []domain.IngestReceipt {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	return append([]domain.IngestReceipt(nil), s.health.failures...)
}

// StorageHealth reads cached state only, even if an insert/probe is hung.
func (s *Store) StorageHealth() domain.StorageHealth {
	h := &s.health
	h.mu.Lock()
	defer h.mu.Unlock()
	v := h.view
	s.recoveryView(&v)
	if v.Storage == "" {
		v.Storage = "unknown"
	}
	progress := h.probe
	if progress.IsZero() {
		progress = h.started
	}
	if h.interval > 0 && !progress.IsZero() && time.Since(progress) > 2*h.interval {
		v.Storage, v.StorageDetail = "failed", "probe_stale"
	}
	for _, r := range h.pending {
		v.IngestLagS = max(v.IngestLagS, int64(time.Since(r.ReceivedAt)/time.Second))
	}
	return v
}

// StartStorageProbe registers shutdown ownership before starting the worker.
func (s *Store) StartStorageProbe(parent context.Context, interval time.Duration) {
	h := &s.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	h.stop, h.done = cancel, make(chan struct{})
	go func() { defer close(h.done); s.RunStorageProbe(ctx, interval) }()
}

// RunStorageProbe performs representative recovery immediately, even on idle restart.
func (s *Store) RunStorageProbe(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	h := &s.health
	h.mu.Lock()
	h.interval, h.started = interval, time.Now()
	h.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.probeStorage(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) probeStorage(parent context.Context) {
	h := &s.health
	h.mu.Lock()
	h.seq++
	seq, recovering := h.seq, h.view.Storage != "ok"
	h.mu.Unlock()
	size := 0
	if recovering {
		size = s.syncHighWater()
		h.mu.Lock()
		h.trackSizeLocked(seq, size)
		h.mu.Unlock()
	}
	err := retryBusy(parent, func(ctx context.Context) error { return s.probeTransaction(ctx, recovering, size) })
	if parent.Err() != nil {
		h.mu.Lock()
		delete(h.sizes, seq)
		h.mu.Unlock()
		return
	}
	h.finish(seq, err, recovering, true)
	s.syncHighWater()
	var fs unix.Statfs_t
	if unix.Statfs(s.dbPath, &fs) == nil && fs.Bsize > 0 {
		h.mu.Lock()
		h.view.FreeBytes = fs.Bavail * uint64(fs.Bsize)
		h.mu.Unlock()
	}
}

// persistBudget is the whole budget for one persistence attempt, BUSY
// retries included.
const persistBudget = 5 * time.Second

// retryBusy retries write while SQLite reports BUSY, within persistBudget.
// Once the budget has expired the attempt is a storage failure whatever the
// interrupted write reports: the deadline can surface as BUSY, as
// SQLITE_INTERRUPT (9), as sql.ErrTxDone or as a context error, depending on
// which side of the driver wins the race, and none of those is a logic
// error. A cancelled or shorter-deadline parent is not our budget and is
// returned unclassified.
func retryBusy(parent context.Context, write func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, persistBudget)
	defer cancel()
	var err error
	for ctx.Err() == nil {
		if err = write(ctx); err == nil {
			return nil
		}
		if sqliteCode(err) != 5 && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if parent.Err() != nil {
		return err
	}
	return busyTimeout{err}
}

func (s *Store) probeTransaction(ctx context.Context, recovering bool, size int) error {
	// Open configures _txlock=immediate on the same history pool/WAL path.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if recovering {
		fixture := StoredMessage{ChatJID: "wad-health@local", SenderJID: "wad-health@local", MessageID: "storage-probe", RawProto: make([]byte, size)}
		var result sql.Result
		result, err = tx.ExecContext(ctx, insertSQL, insertValues(fixture)...)
		if err != nil {
			return err
		}
		var n int64
		if n, err = result.RowsAffected(); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("sqlitehistory: recovery fixture collision")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM messages WHERE chat_jid = ? AND message_id = ?", fixture.ChatJID, fixture.MessageID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO health_probe(id,wrote_at) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET wrote_at=excluded.wrote_at", time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
