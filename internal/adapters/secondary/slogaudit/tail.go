package slogaudit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"
)

// Entry is one decoded audit line (spec 116 FR-116-06 audit tail).
type Entry struct {
	EventTime time.Time `json:"event_time"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Subject   string    `json:"subject"`
	Decision  string    `json:"decision"`
	Detail    string    `json:"detail"`
	Source    string    `json:"source"`
}

// Tail returns the last n records of the live file, newest first. A line
// that does not decode is returned with Detail naming it, never dropped,
// so tampering shows as a row rather than a gap. It holds the write lock,
// so a line being appended is never read half-written.
//
// ponytail: scans the whole live file (Rotate bounds it); seek from the
// end if the live file ever grows past a few MB.
func (a *Audit) Tail(n int) ([]Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.Open(a.path)
	if err != nil {
		return nil, fmt.Errorf("slogaudit tail: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ring := make([]Entry, 0, n)
	for line := 1; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			e = Entry{Action: "unreadable", Detail: fmt.Sprintf("line %d does not decode", line)}
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("slogaudit tail: %w", err)
	}
	slices.Reverse(ring)
	return ring, nil
}

// Verify runs VerifyChain over the live file under the write lock.
func (a *Audit) Verify() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return VerifyChain(a.path, a.chain.key)
}
