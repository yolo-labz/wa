package sqlitehistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	seq, failed              uint64
	probe, started, received time.Time
	interval                 time.Duration
	pending                  map[uint64]domain.IngestReceipt
	failures                 []domain.IngestReceipt
	maxRecord                int
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
	h.mu.Unlock()
	size := 65536
	for _, m := range msgs {
		size = max(size, len(m.RawProto)+len(m.Body)+len(m.Caption)+len(m.InteractiveJSON)+len(m.ChatJID)+len(m.SenderJID)+len(m.MessageID)+len(m.PushName)+len(m.SenderAltJID)+len(m.AddressingMode)+len(m.MediaType)+1024)
	}
	_, err := s.mergeHighWater(size, true)
	return seq, err
}

// Persist an upper bound BEFORE attempting the corresponding insert. Fixed-width
// monotonic overwrites cannot shrink an older bound even after a partial write.
func (s *Store) mergeHighWater(size int, persist bool) (int, error) {
	h := &s.health
	h.journal.Lock()
	defer h.journal.Unlock()
	b, err := os.ReadFile(s.dbPath + ".health-size")
	disk := 0
	if err == nil {
		disk, err = strconv.Atoi(strings.TrimSpace(string(b)))
		if disk < 65536 {
			err = errors.New("invalid health size journal")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	size = max(size, disk, h.maxRecord, 65536)
	h.mu.Unlock()
	if persist && size > disk {
		f, err := os.OpenFile(s.dbPath+".health-size", os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		text := strconv.Itoa(size)
		_, err = f.WriteString(strings.Repeat("0", 20-len(text)) + text + "\n")
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return 0, err
		}
	}
	h.mu.Lock()
	h.maxRecord = size
	h.mu.Unlock()
	return size, nil
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
	if err != nil {
		if live {
			h.failures = append(h.failures, receipt)
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
	}
	if changed && seq > h.failed {
		h.view.Storage = "ok"
		return true
	}
	return false
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
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	size, err := 0, error(nil)
	if recovering {
		size, err = s.mergeHighWater(65536, false)
	}
	if err == nil {
		err = retryBusy(ctx, func() error { return s.probeTransaction(ctx, recovering, size) })
	}
	if parent.Err() != nil {
		return
	}
	h.finish(seq, err, recovering, true)
	var fs unix.Statfs_t
	if unix.Statfs(s.dbPath, &fs) == nil && fs.Bsize > 0 {
		h.mu.Lock()
		h.view.FreeBytes = fs.Bavail * uint64(fs.Bsize)
		h.mu.Unlock()
	}
}

func retryBusy(ctx context.Context, write func() error) error {
	started := time.Now()
	var busy error
	for {
		err := ctx.Err()
		if err == nil {
			err = write()
		}
		if sqliteCode(err) == 5 {
			busy = err
		}
		if busy != nil && (sqliteCode(err) == 5 || errors.Is(err, context.DeadlineExceeded)) && time.Since(started) >= 5*time.Second && !errors.Is(ctx.Err(), context.Canceled) {
			return busyTimeout{busy}
		}
		if sqliteCode(err) != 5 {
			return err
		}
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
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
