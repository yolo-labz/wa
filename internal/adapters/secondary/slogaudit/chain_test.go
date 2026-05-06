package slogaudit_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestVerifyChain_HappyPath pins the spec 016 FR-015 / T055 contract:
// a fresh audit log produces a valid HMAC chain across 3 records,
// VerifyChain accepts it, and the verified count matches.
func TestVerifyChain_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now().UTC()
	for i, action := range []domain.AuditAction{domain.AuditSend, domain.AuditGrant, domain.AuditPair} {
		ev := domain.NewAuditEventFrom("test", "tester", action, domain.JID{}, "ok", "msg")
		ev.TS = now.Add(time.Duration(i+1) * time.Millisecond)
		if err := a.Record(context.Background(), ev); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	keyData, err := os.ReadFile(a.KeyPath())
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	key, err := decodeHexKey(keyData)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}

	verified, err := slogaudit.VerifyChain(path, key)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if verified != 3 {
		t.Fatalf("verified = %d, want 3", verified)
	}
}

// TestVerifyChain_TamperDetected pins the tamper-evidence contract: a
// single byte flipped inside any line breaks VerifyChain.
func TestVerifyChain_TamperDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now().UTC()
	for i := range 3 {
		ev := domain.NewAuditEventFrom("test", "tester", domain.AuditSend, domain.JID{}, "ok", "before")
		ev.TS = now.Add(time.Duration(i+1) * time.Millisecond)
		if err := a.Record(context.Background(), ev); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Mutate the middle line: replace "before" with "after" (same length
	// so byte-offsets shift cleanly).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(raw), "before", "after_", 1) // 6 bytes → 6 bytes
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	keyData, err := os.ReadFile(a.KeyPath())
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	key, _ := decodeHexKey(keyData)

	if _, err := slogaudit.VerifyChain(path, key); err == nil {
		t.Fatal("VerifyChain accepted tampered log; want hmac mismatch")
	}
}

// TestVerifyChain_RestartContinuesChain pins that closing and re-Open-ing
// the audit log preserves the HMAC chain across restarts.
func TestVerifyChain_RestartContinuesChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a1, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	now := time.Now().UTC()
	ev := domain.NewAuditEventFrom("test", "a1", domain.AuditSend, domain.JID{}, "ok", "first")
	ev.TS = now
	if err := a1.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record a1: %v", err)
	}
	keyPath := a1.KeyPath()
	if err := a1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Re-open and append.
	a2, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	ev2 := domain.NewAuditEventFrom("test", "a2", domain.AuditPair, domain.JID{}, "ok", "second")
	ev2.TS = now.Add(2 * time.Millisecond)
	if err := a2.Record(context.Background(), ev2); err != nil {
		t.Fatalf("Record a2: %v", err)
	}
	if err := a2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}

	keyData, _ := os.ReadFile(keyPath)
	key, _ := decodeHexKey(keyData)
	verified, err := slogaudit.VerifyChain(path, key)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if verified != 2 {
		t.Fatalf("verified = %d, want 2 (chain spans restart)", verified)
	}
}

func decodeHexKey(data []byte) ([]byte, error) {
	s := strings.TrimSpace(string(data))
	out := make([]byte, len(s)/2)
	for i := range out {
		var b byte
		for j := range 2 {
			c := s[i*2+j]
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= c - '0'
			case c >= 'a' && c <= 'f':
				b |= c - 'a' + 10
			case c >= 'A' && c <= 'F':
				b |= c - 'A' + 10
			default:
				return nil, &decodeErr{c}
			}
		}
		out[i] = b
	}
	return out, nil
}

type decodeErr struct{ c byte }

func (e *decodeErr) Error() string { return "invalid hex char " + string(e.c) }
