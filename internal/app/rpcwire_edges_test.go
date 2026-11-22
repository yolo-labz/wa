package app_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

type wideCoded struct{ code int }

func (e wideCoded) Error() string { return "wide code detail /var/lib/wa/secret.db" }
func (e wideCoded) RPCCode() int  { return e.code }

// TestRPCWire_NilAndOutOfRange: no error maps to the zero triple; a coded
// error whose int code does not fit int32 degrades to the opaque internal
// error (no wraparound, no detail text), also when wrapped.
func TestRPCWire_NilAndOutOfRange(t *testing.T) {
	t.Parallel()
	if c, m, r := app.RPCWire(nil); c != 0 || m != "" || r != "" {
		t.Errorf("RPCWire(nil) = %d %q %q, want zero", c, m, r)
	}
	if math.MaxInt != math.MaxInt32 { // 64-bit build: out-of-range values exist
		for _, code := range []int{math.MaxInt32 + 1, math.MinInt32 - 1, 1 << 40} {
			c, m, r := app.RPCWire(fmt.Errorf("ctx: %w", wideCoded{code}))
			if c != -32603 || m != app.MsgInternalError || r != domain.ReasonInternal {
				t.Errorf("code %d = %d %q %q, want the opaque internal error", code, c, m, r)
			}
		}
	}
	// In range: the coded value's own text and code are kept, reason stays internal without RPCReason.
	if c, m, r := app.RPCWire(wideCoded{-32050}); c != -32050 || m != (wideCoded{}).Error() || r != domain.ReasonInternal {
		t.Errorf("in-range = %d %q %q", c, m, r)
	}
	for _, code := range []int{math.MaxInt32, math.MinInt32} {
		if c, _, _ := app.RPCWire(wideCoded{code}); int(c) != code {
			t.Errorf("boundary code %d became %d", code, c)
		}
	}
}
