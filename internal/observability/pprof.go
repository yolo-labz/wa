package observability

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"runtime/pprof"
	"time"
)

// PprofProfileKind is one of the runtime profile families the debug
// method exposes. "cpu" writes a sampling CPU profile over a caller-
// supplied window; the others are snapshot profiles.
type PprofProfileKind string

// The five supported profile kinds. "cpu" is a sampling window; the
// rest are synchronous snapshots served from the runtime profile
// registry.
const (
	PprofCPU       PprofProfileKind = "cpu"
	PprofHeap      PprofProfileKind = "heap"
	PprofGoroutine PprofProfileKind = "goroutine"
	PprofBlock     PprofProfileKind = "block"
	PprofMutex     PprofProfileKind = "mutex"
)

// pprofParams is the JSON-RPC params accepted by debug.pprof.profile.
type pprofParams struct {
	Kind    string `json:"kind"`
	Seconds int    `json:"seconds,omitempty"`
}

// pprofResult is the JSON-RPC result: base64-encoded profile bytes plus
// the decoded byte count for quick sanity checks on the CLI side.
type pprofResult struct {
	Profile string `json:"profile"`
	Bytes   int    `json:"bytes"`
	Kind    string `json:"kind"`
}

const (
	// pprofMaxSeconds caps the CPU-profile window. Longer windows would
	// pin the sampler and block shutdown drain past its deadline.
	pprofMaxSeconds = 120
	// pprofDefaultSeconds is the CPU-profile window when the caller
	// omits `seconds`.
	pprofDefaultSeconds = 30
)

// ErrPprofUnknownKind reports an unsupported profile name.
var ErrPprofUnknownKind = errors.New("unknown pprof kind")

// PprofHandler is the JSON-RPC method handler registered as
// debug.pprof.profile. It collects a runtime/pprof profile and returns
// a base64 blob; the CLI decodes it to a temp file and shells out to
// `go tool pprof`.
//
// No net/http/pprof dependency is introduced — the TCP surface would
// violate constitution §IV.21 (no ad-hoc network listeners in wad).
func PprofHandler(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p pprofParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("pprof: parse params: %w", err)
		}
	}
	kind := PprofProfileKind(p.Kind)

	buf, err := collectPprof(ctx, kind, p.Seconds)
	if err != nil {
		return nil, err
	}

	out := pprofResult{
		Profile: base64.StdEncoding.EncodeToString(buf),
		Bytes:   len(buf),
		Kind:    string(kind),
	}
	return json.Marshal(out)
}

// collectPprof runs the requested profile into an in-memory buffer.
func collectPprof(ctx context.Context, kind PprofProfileKind, seconds int) ([]byte, error) {
	var buf bytes.Buffer
	switch kind {
	case PprofCPU:
		if seconds <= 0 {
			seconds = pprofDefaultSeconds
		}
		if seconds > pprofMaxSeconds {
			return nil, fmt.Errorf("pprof: seconds=%d exceeds max %d", seconds, pprofMaxSeconds)
		}
		if err := pprof.StartCPUProfile(&buf); err != nil {
			return nil, fmt.Errorf("pprof: start cpu: %w", err)
		}
		select {
		case <-ctx.Done():
			pprof.StopCPUProfile()
			return nil, ctx.Err()
		case <-time.After(time.Duration(seconds) * time.Second):
		}
		pprof.StopCPUProfile()
		return buf.Bytes(), nil

	case PprofHeap, PprofGoroutine, PprofBlock, PprofMutex:
		// Force GC before heap snapshots so the output reflects live
		// allocations rather than uncollected garbage.
		if kind == PprofHeap {
			runtime.GC()
		}
		prof := pprof.Lookup(string(kind))
		if prof == nil {
			return nil, fmt.Errorf("%w: %s", ErrPprofUnknownKind, kind)
		}
		if err := prof.WriteTo(&buf, 0); err != nil {
			return nil, fmt.Errorf("pprof: write %s: %w", kind, err)
		}
		return buf.Bytes(), nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrPprofUnknownKind, kind)
	}
}
