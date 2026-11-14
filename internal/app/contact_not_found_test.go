package app_test

import (
	"fmt"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// contacts.lookup wraps the directory error with %w; the wire must still
// carry the typed -32119 and its reason, never the opaque -32603.
func TestRPCWire_ContactNotFoundIsTyped(t *testing.T) {
	code, _, reason := app.RPCWire(fmt.Errorf("contacts.lookup: %w: x", app.ErrContactNotFound))
	if code != -32119 || reason != domain.ReasonInvalidParams {
		t.Fatalf("RPCWire = %d, %q; want -32119, %q", code, reason, domain.ReasonInvalidParams)
	}
}
