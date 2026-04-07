package domain

import (
	"errors"
	"fmt"
	"testing"
)

// TestSentinelWrap asserts that each sentinel in errors.go survives an
// fmt.Errorf("%w") wrap and is recoverable via errors.Is. This is the
// behavioural guarantee callers rely on to branch on error category.
func TestSentinelWrap(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
	}{
		{"ErrDisconnected", ErrDisconnected},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("send: %w", tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(fmt.Errorf(\"send: %%w\", %s), %s) = false, want true", tc.name, tc.name)
			}
		})
	}
}
