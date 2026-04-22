// Package observability owns the OpenTelemetry shim, span helpers,
// metric instruments, pprof JSON-RPC handler, and crash-dump wiring
// for wad. Imported by adapters and use cases; depends only on OTel
// SDK + stdlib, never on whatsmeow.
package observability

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// jidSalt is a per-process random salt mixed into every hashed JID.
// Prevents rainbow-table reversal if telemetry leaks, and keeps the
// same JID hashed consistently within a run so traces correlate.
var (
	jidSaltOnce sync.Once
	jidSalt     []byte
)

// initJIDSalt lazily seeds jidSalt with 32 bytes of crypto/rand.
// Go 1.24+ guarantees crypto/rand.Read never returns an error on
// supported OSes (release notes, crypto/rand), so we rely on that
// contract rather than plumbing an error up through HashJID.
func initJIDSalt() {
	jidSaltOnce.Do(func() {
		jidSalt = make([]byte, 32)
		_, _ = rand.Read(jidSalt)
	})
}

// HashJID returns a stable 16-hex-char fingerprint of jid under the
// per-process salt. Empty input returns empty string so callers can
// blindly pass `SetAttributes(semconv.String("wa.jid", HashJID(j)))`
// without a nil check.
//
// The hash is SHA-256 truncated to 64 bits of output (16 hex chars).
// 64 bits is enough to collide only at ~4 billion distinct JIDs per
// process — a personal WhatsApp account will never approach that.
func HashJID(jid string) string {
	if jid == "" {
		return ""
	}
	initJIDSalt()
	h := sha256.New()
	h.Write(jidSalt)
	h.Write([]byte(jid))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// resetJIDSaltForTest is the test-only salt reset. Lives in the prod
// file (not _test.go) because observability_test is in a separate
// package and can't touch unexported once-guards otherwise.
func resetJIDSaltForTest() {
	jidSaltOnce = sync.Once{}
	jidSalt = nil
}
