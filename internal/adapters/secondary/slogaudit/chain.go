package slogaudit

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// hmacChainWriter wraps the underlying audit-log file with an HMAC
// hash-chain stamp on every line. Spec 016 FR-015 / T053 — OWASP
// A09:2025 tamper-evident audit trail.
//
// On every Write call from slog (one Write per record line, including
// the trailing newline) the writer:
//
//  1. Strips the trailing newline and the closing "}".
//  2. Computes HMAC-SHA-256 over (prev_hmac_hex || trimmed_line) using
//     the chain key.
//  3. Appends `,"prev":"<prev_hex>","hmac":"<this_hex>"` (always in
//     that exact order, even when prev is empty), closes with "}",
//     and writes the result to the underlying file.
//  4. Stores `<this_hex>` as the new `prev_hmac` for the next line.
//
// The fixed insertion order — `,"prev":...` immediately followed by
// `,"hmac":...` immediately before the closing `}` — is the contract
// VerifyChain relies on to reconstruct the bytes the HMAC was computed
// over without re-parsing JSON (which would lose attribute order).
type hmacChainWriter struct {
	f    io.Writer
	key  []byte
	last string // hex of the last HMAC, "" before the first line
}

func (w *hmacChainWriter) Write(p []byte) (int, error) {
	// slog.JSONHandler always writes one full line, terminated with
	// '\n'. Be defensive: if a line ever arrives without a trailing
	// '}' (rotation half-flush, runaway custom handler) write it
	// straight through and skip chaining for that line.
	trimmed := bytes.TrimRight(p, "\n")
	if !bytes.HasSuffix(trimmed, []byte("}")) {
		return w.f.Write(p)
	}
	body := trimmed[:len(trimmed)-1] // drop the closing "}"

	mac := hmac.New(sha256.New, w.key)
	mac.Write([]byte(w.last))
	mac.Write(body)
	sum := hex.EncodeToString(mac.Sum(nil))

	var out bytes.Buffer
	out.Grow(len(p) + 256)
	out.Write(body)
	out.WriteString(`,"prev":"`)
	out.WriteString(w.last)
	out.WriteString(`","hmac":"`)
	out.WriteString(sum)
	out.WriteString(`"}` + "\n")

	if _, err := w.f.Write(out.Bytes()); err != nil {
		return 0, err
	}
	w.last = sum
	// Mask the actual byte count from slog: it just needs to know the
	// caller-visible "non-error". Returning len(p) keeps slog happy.
	return len(p), nil
}

// loadOrCreateKey reads a 32-byte hex-encoded HMAC key from keyPath,
// creating it with crypto/rand on first use. Codex review §MINOR (6):
// creation is race-safe via O_CREATE|O_EXCL — two daemons starting
// simultaneously will see exactly one create and the loser falls
// through to ReadFile. File mode 0600.
func loadOrCreateKey(keyPath string) ([]byte, error) {
	for attempt := range 2 {
		if data, err := os.ReadFile(keyPath); err == nil { //nolint:gosec // path from caller, file mode 0600
			key, err := hex.DecodeString(string(bytes.TrimSpace(data)))
			if err != nil {
				return nil, fmt.Errorf("slogaudit: parse key %s: %w", keyPath, err)
			}
			if len(key) != 32 {
				return nil, fmt.Errorf("slogaudit: key %s wrong length %d, want 32", keyPath, len(key))
			}
			return key, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("slogaudit: read key %s: %w", keyPath, err)
		}
		if attempt > 0 {
			return nil, fmt.Errorf("slogaudit: key %s repeatedly disappeared between probes", keyPath)
		}

		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("slogaudit: generate key: %w", err)
		}
		f, err := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // 0600 is the intended mode
		if errors.Is(err, os.ErrExist) {
			// Another process won the race; loop back to ReadFile.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("slogaudit: create key %s: %w", keyPath, err)
		}
		if _, werr := f.Write([]byte(hex.EncodeToString(key) + "\n")); werr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("slogaudit: write key %s: %w", keyPath, werr)
		}
		if cerr := f.Close(); cerr != nil {
			return nil, fmt.Errorf("slogaudit: close key %s: %w", keyPath, cerr)
		}
		return key, nil
	}
	return nil, fmt.Errorf("slogaudit: load key %s: gave up after retry loop", keyPath)
}

// VerifyChain walks the audit log at path and validates every line's
// HMAC against the recorded chain. Returns the number of verified
// records and any error. A non-nil error names the offending line
// number plus the discrepancy (prev mismatch, hmac mismatch, malformed
// line). Spec 016 FR-015 / T054.
//
// Empty file returns (0, nil). Lines without an `hmac` field are
// treated as pre-chain history and skipped — they break the chain at
// that point but do NOT fail verification, since pre-T053 audit logs
// are still legitimate.
func VerifyChain(path string, key []byte) (int, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return 0, fmt.Errorf("slogaudit verify: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	verified := 0
	prev := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Find the trailing `,"prev":"<hex>","hmac":"<hex>"}` suffix.
		// The exact insertion contract from hmacChainWriter pins the
		// `prev` field immediately before `hmac`, both immediately
		// before the closing `}`, so a fixed string-find is safe.
		prevTag := []byte(`,"prev":"`)
		idx := bytes.LastIndex(line, prevTag)
		if idx < 0 {
			// Pre-chain line — chain restarts after it.
			prev = ""
			continue
		}
		// body = the bytes the HMAC was computed over.
		body := line[:idx]
		// Tail = everything from `,"prev"` onward, without the closing `}`.
		tail := line[idx : len(line)-1]
		// Parse "<prev>" and "<hmac>" out of tail by exact slicing.
		// tail = `,"prev":"<a>","hmac":"<b>"`
		recordedPrev, recordedHMAC, perr := parsePrevHmacTail(tail)
		if perr != nil {
			return verified, fmt.Errorf("slogaudit verify: line %d: %w", lineNo, perr)
		}
		if recordedPrev != prev {
			return verified, fmt.Errorf("slogaudit verify: line %d: prev mismatch (have %q, recorded %q)",
				lineNo, prev, recordedPrev)
		}

		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(prev))
		mac.Write(body)
		got := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(got), []byte(recordedHMAC)) {
			return verified, fmt.Errorf("slogaudit verify: line %d: hmac mismatch", lineNo)
		}

		prev = recordedHMAC
		verified++
	}
	if err := scanner.Err(); err != nil {
		return verified, fmt.Errorf("slogaudit verify: scan %s: %w", path, err)
	}
	return verified, nil
}

// parsePrevHmacTail extracts the prev + hmac hex strings from a tail
// of the exact form `,"prev":"<a>","hmac":"<b>"`. Returns an error if
// the structure does not match.
func parsePrevHmacTail(tail []byte) (prev, mac string, err error) {
	const prevPrefix = `,"prev":"`
	const sep = `","hmac":"`
	const suffix = `"`
	if !bytes.HasPrefix(tail, []byte(prevPrefix)) {
		return "", "", errors.New("malformed: missing prev prefix")
	}
	rest := tail[len(prevPrefix):]
	sepIdx := bytes.Index(rest, []byte(sep))
	if sepIdx < 0 {
		return "", "", errors.New("malformed: missing hmac separator")
	}
	prev = string(rest[:sepIdx])
	rest = rest[sepIdx+len(sep):]
	if !bytes.HasSuffix(rest, []byte(suffix)) {
		return "", "", errors.New("malformed: missing trailing quote")
	}
	mac = string(rest[:len(rest)-len(suffix)])
	return prev, mac, nil
}
