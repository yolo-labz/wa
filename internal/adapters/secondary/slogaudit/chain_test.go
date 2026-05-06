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

// TestRestart_RefusesPartialTail pins the Codex §MAJOR (5) fix:
// a daemon crash mid-line leaves the audit log without a trailing
// newline. The restart MUST refuse to seed `chain.last` from the
// truncated bytes — otherwise an attacker who can induce mid-line
// truncation could reset the chain and forge subsequent records
// against an unauthenticated head. After such a crash the chain
// restarts from genesis (last="").
func TestRestart_RefusesPartialTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now().UTC()
	ev := domain.NewAuditEventFrom("test", "tester", domain.AuditSend, domain.JID{}, "ok", "first")
	ev.TS = now
	if err := a.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a crash by stripping the trailing newline AND a few
	// bytes of the closing `}"`-suffix.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) < 5 {
		t.Fatalf("audit log too short: %d bytes", len(raw))
	}
	truncated := raw[:len(raw)-5] // drop trailing `}"\n` and a couple bytes
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Re-open: the restart MUST NOT panic and MUST NOT seed from the
	// partial line. Append a new record; the new chain has prev="".
	a2, err := slogaudit.Open(path)
	if err != nil {
		t.Fatalf("Open after truncate: %v", err)
	}
	ev2 := domain.NewAuditEventFrom("test", "tester", domain.AuditPair, domain.JID{}, "ok", "after-crash")
	ev2.TS = now.Add(time.Second)
	if err := a2.Record(context.Background(), ev2); err != nil {
		t.Fatalf("Record after truncate: %v", err)
	}
	if err := a2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}

	// The restart line should have prev="" — confirm by reading the
	// last line of the file and checking the suffix.
	raw2, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(raw2), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"prev":""`) {
		t.Fatalf("post-truncate restart did not start a fresh chain; last line: %s", last)
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
