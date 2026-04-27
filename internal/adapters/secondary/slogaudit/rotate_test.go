package slogaudit_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/internal/domain"
)

// TestAuditRotateAtomicRename asserts Rotate renames the live file to a
// timestamped sibling and opens a fresh 0600 file at the original path.
func TestAuditRotateAtomicRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = a.Close() }()

	jid, err := domain.Parse("15551234567@s.whatsapp.net")
	if err != nil {
		t.Fatalf("jid: %v", err)
	}
	for i := range 3 {
		e := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "seed")
		e.TS = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		if err := a.Record(context.Background(), e); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	rotated, err := a.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !strings.HasPrefix(rotated, path+".") {
		t.Errorf("rotated = %q, want prefix %q", rotated, path+".")
	}

	rinfo, err := os.Stat(rotated)
	if err != nil {
		t.Fatalf("stat rotated: %v", err)
	}
	if rinfo.Size() == 0 {
		t.Errorf("rotated file empty, expected 3 audit lines")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat new: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("new file perm = %o, want 0600", perm)
	}
	if info.Size() != 0 {
		t.Errorf("new file non-empty (%d bytes)", info.Size())
	}
}

// TestAuditRotateRecordAfterRotateHitsNewFile asserts writes after
// rotation land in the fresh file and leave the rotated file untouched.
func TestAuditRotateRecordAfterRotateHitsNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = a.Close() }()

	jid, _ := domain.Parse("15551234567@s.whatsapp.net")
	first := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "first")
	first.TS = time.Now().UTC()
	if err := a.Record(context.Background(), first); err != nil {
		t.Fatalf("record: %v", err)
	}

	rotated, err := a.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	rotatedSizeBefore, _ := os.Stat(rotated)

	second := domain.NewAuditEvent("wad", domain.AuditSend, jid, "allow", "second")
	second.TS = time.Now().UTC().Add(time.Second)
	if err := a.Record(context.Background(), second); err != nil {
		t.Fatalf("record post-rotate: %v", err)
	}

	rotatedSizeAfter, _ := os.Stat(rotated)
	if rotatedSizeBefore.Size() != rotatedSizeAfter.Size() {
		t.Errorf("rotated file grew: %d -> %d", rotatedSizeBefore.Size(), rotatedSizeAfter.Size())
	}

	newSize, _ := os.Stat(path)
	if newSize.Size() == 0 {
		t.Errorf("new audit.log empty after post-rotate record")
	}
}
