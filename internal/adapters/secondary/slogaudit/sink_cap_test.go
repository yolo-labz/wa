package slogaudit_test

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func openAudit(t *testing.T) (*slogaudit.Audit, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, path
}

func record(t *testing.T, a *slogaudit.Audit, i int, detail string) {
	t.Helper()
	e := domain.NewAuditEvent("wad", domain.AuditSend, domain.JID{}, "ok", detail)
	e.TS = time.Now().UTC().Add(time.Duration(i) * time.Microsecond)
	if err := a.Record(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func TestSlogaudit_OversizeDetailTruncated(t *testing.T) {
	a, path := openAudit(t)
	record(t, a, 0, "first")
	record(t, a, 1, strings.Repeat("A", 5<<20))
	record(t, a, 2, "last")
	if st, _ := os.Stat(path); st.Size() > 16<<10 {
		t.Fatalf("a 5 MiB detail grew the log to %d bytes", st.Size())
	}
	es, err := a.Tail(10)
	if err != nil || len(es) != 3 || !strings.HasSuffix(es[1].Detail, "[truncated]") {
		t.Fatalf("tail = %d rows, err %v, detail tail %q", len(es), err, es[len(es)/2].Detail[len(es[len(es)/2].Detail)-20:])
	}
	if n, err := a.Verify(); err != nil || n != 3 {
		t.Fatalf("verify = %d, %v", n, err)
	}
}

func TestSlogaudit_TailSkipsOverlongLine(t *testing.T) {
	a, path := openAudit(t)
	record(t, a, 0, "before")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"detail":"` + strings.Repeat("B", 3<<20) + `"}` + "\n")
	_ = f.Close()
	record(t, a, 1, "after")
	es, err := a.Tail(10)
	if err != nil || len(es) != 3 {
		t.Fatalf("tail = %d rows, err %v; the oversize line must be a row, not a failure", len(es), err)
	}
	if es[0].Detail != "after" || es[1].Action != "unreadable" || !strings.Contains(es[1].Detail, "line 2") || es[2].Detail != "before" {
		t.Fatalf("rows = %+v", es)
	}
}

func TestVerifyChain_OversizeLineNamesTheLine(t *testing.T) {
	a, path := openAudit(t)
	record(t, a, 0, "ok")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	chunk := strings.Repeat("C", 1<<20)
	_, _ = f.WriteString(`{"detail":"`)
	for range 33 {
		_, _ = f.WriteString(chunk)
	}
	_, _ = f.WriteString(`"}` + "\n")
	_ = f.Close()
	raw, _ := os.ReadFile(a.KeyPath())
	k, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = slogaudit.VerifyChain(path, k)
	if err == nil || !strings.Contains(err.Error(), "line 2: longer than") {
		t.Fatalf("err = %v, want a 'line 2: longer than' error rather than a scanner failure", err)
	}
}

func TestAudit_TailDoesNotBlockRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	var b strings.Builder
	line := `{"time":"2026-01-01T00:00:00Z","msg":"audit","actor":"x","action":"send","decision":"ok","detail":"` + strings.Repeat("d", 200) + `"}` + "\n"
	for range 150_000 { // ~32 MB of pre-chain history
		b.WriteString(line)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = a.Tail(200) }()
	var during int
	for i := 0; ; i++ {
		select {
		case <-done:
			if during < 3 {
				t.Fatalf("only %d audit appends completed while Tail scanned; Tail is blocking Record", during)
			}
			return
		default:
			record(t, a, i+1, "during")
			during++
		}
	}
}
