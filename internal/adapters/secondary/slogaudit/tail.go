package slogaudit

import (
	"bufio"
	"bytes"
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

// maxTailLine bounds what Tail will decode; longer lines are shown as
// unreadable rows. maxVerifyLine bounds VerifyChain, which must still be
// able to check a line an older writer appended without a cap.
const (
	maxTailLine   = 1 << 20
	maxVerifyLine = 32 << 20
)

// Tail returns the last n records of the live file, newest first. A line
// that does not decode (or is over maxTailLine) is returned with Detail
// naming it, never dropped, so tampering shows as a row rather than a gap.
//
// It reads the file as it was when the lock was taken: Record appends whole
// lines under the same lock, so that size is a line boundary, and the scan
// itself runs outside the lock and never stalls an audit append.
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

// snapshot opens the live file and reports its size and the chain key
// under the write lock, so a reader sees a prefix of whole lines.
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

// readLine returns the next line without its newline. A line longer than
// maxLen is consumed whole and reported as tooLong with a nil line, so one
// oversize line neither ends the walk nor exhausts memory.
func readLine(br *bufio.Reader, maxLen int) (line []byte, tooLong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxLen {
				line, tooLong = nil, true
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case errors.Is(e, bufio.ErrBufferFull):
			continue
		case errors.Is(e, io.EOF) && len(line) == 0 && !tooLong:
			return nil, false, io.EOF
		case e != nil && !errors.Is(e, io.EOF):
			return nil, false, e
		}
		return bytes.TrimSuffix(line, []byte("\n")), tooLong, nil
	}
}

// Verify runs the chain check over the same whole-line snapshot Tail uses.
func (a *Audit) Verify() (int, error) {
	f, size, key, err := a.snapshot()
	if err != nil {
		return 0, fmt.Errorf("slogaudit verify: open %s: %w", a.path, err)
	}
	defer func() { _ = f.Close() }()
	return verifyReader(io.LimitReader(f, size), key)
}
