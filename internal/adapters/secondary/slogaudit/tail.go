package slogaudit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// Line limits: Tail shows a longer line as an unreadable row; VerifyChain
// still checks lines an older, uncapped writer appended.
const (
	maxTailLine   = 1 << 20
	maxVerifyLine = 32 << 20
)

// Tail returns the last n records of the live file, newest first. A line
// that does not decode (or is over maxTailLine) is returned with Detail
// naming it, never dropped, so tampering shows as a row rather than a gap.
// The scan runs on a size snapshot taken under the lock (a line boundary,
// since Record appends whole lines), so it never stalls an audit append.
//
// ponytail: scans the whole live file (Rotate bounds it); seek from the
// end if the live file ever grows past a few MB.
func (a *Audit) Tail(n int) ([]Entry, error) {
	f, size, _, err := a.snapshot()
	if err != nil {
		return nil, fmt.Errorf("slogaudit tail: %w", err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(io.LimitReader(f, size))
	ring := make([]Entry, 0, n)
	for line := 1; ; line++ {
		b, long, err := readLine(br, maxTailLine)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("slogaudit tail: %w", err)
		}
		if len(b) == 0 && !long {
			continue
		}
		var e Entry
		if long || json.Unmarshal(b, &e) != nil {
			e = Entry{Action: "unreadable", Detail: fmt.Sprintf("line %d does not decode", line)}
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, e)
	}
	slices.Reverse(ring)
	return ring, nil
}

// snapshot opens the live file and reports its size and the chain key.
func (a *Audit) snapshot() (*os.File, int64, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.Open(a.path)
	if err != nil {
		return nil, 0, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, nil, err
	}
	return f, st.Size(), a.chain.key, nil
}

// readLine returns the next line without its newline. A line over maxLen
// is consumed whole and reported as tooLong, so one oversize line neither
// ends the walk nor exhausts memory.
func readLine(br *bufio.Reader, maxLen int) (line []byte, tooLong bool, err error) {
	for {
		chunk, more, err := br.ReadLine()
		if err != nil {
			return nil, false, err
		}
		if tooLong || len(line)+len(chunk) > maxLen {
			line, tooLong = nil, true
		} else {
			line = append(line, chunk...)
		}
		if !more {
			return line, tooLong, nil
		}
	}
}

// Verify runs the chain check over the same snapshot.
func (a *Audit) Verify() (int, error) {
	f, size, key, err := a.snapshot()
	if err != nil {
		return 0, fmt.Errorf("slogaudit verify: open %s: %w", a.path, err)
	}
	defer func() { _ = f.Close() }()
	return verifyReader(io.LimitReader(f, size), key)
}
