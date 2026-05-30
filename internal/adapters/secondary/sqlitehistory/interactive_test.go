package sqlitehistory_test

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestInteractivePersistRoundTrip is the issue #201 persistence proof: an
// inbound message carrying an InteractivePayload is persisted via
// InsertRawInteractive, read back via QueryHistory, and the recovered
// interactive_json BLOB is non-NULL and decodes to the same selection.
func TestInteractivePersistRoundTrip(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	chat := "558134658209@s.whatsapp.net"
	sender := "558199999999@s.whatsapp.net"

	payload := &domain.InteractivePayload{
		Subtype: domain.InteractiveList,
		Prompt:  "Choose a unit",
		Options: []domain.InteractiveOption{{ID: "unit_recife_riomar", Label: "Unidade Recife - Rio Mar"}},
	}
	blob, err := sqlitehistory.MarshalInteractive(payload)
	if err != nil {
		t.Fatalf("MarshalInteractive: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("MarshalInteractive returned empty blob for non-nil payload")
	}

	if err := s.InsertRawInteractive(ctx,
		chat, sender, "MSG-INTERACTIVE-1", 1_700_000_000,
		"Choose a unit", "", "", "Cliente", false, nil,
		"", "", blob,
	); err != nil {
		t.Fatalf("InsertRawInteractive: %v", err)
	}

	got, err := s.QueryHistory(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryHistory returned %d rows, want 1", len(got))
	}
	if len(got[0].InteractiveJSON) == 0 {
		t.Fatal("interactive_json is NULL/empty after round-trip; want non-NULL")
	}

	decoded, err := sqlitehistory.UnmarshalInteractive(got[0].InteractiveJSON)
	if err != nil {
		t.Fatalf("UnmarshalInteractive: %v", err)
	}
	if decoded == nil {
		t.Fatal("UnmarshalInteractive returned nil for persisted payload")
	}
	if decoded.Subtype != domain.InteractiveList {
		t.Errorf("subtype = %v, want InteractiveList", decoded.Subtype)
	}
	if decoded.Prompt != "Choose a unit" {
		t.Errorf("prompt = %q, want %q", decoded.Prompt, "Choose a unit")
	}
	if len(decoded.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(decoded.Options))
	}
	if decoded.Options[0].ID != "unit_recife_riomar" {
		t.Errorf("option.ID = %q, want %q", decoded.Options[0].ID, "unit_recife_riomar")
	}
	if decoded.Options[0].Label != "Unidade Recife - Rio Mar" {
		t.Errorf("option.Label = %q, want %q", decoded.Options[0].Label, "Unidade Recife - Rio Mar")
	}
}

// TestInteractivePersistNullForOrdinaryMessage confirms an ordinary message
// (InsertRaw, no interactive payload) round-trips with a NULL/empty
// interactive_json — the column must not spuriously populate (issue #201).
func TestInteractivePersistNullForOrdinaryMessage(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	chat := "558134658209@s.whatsapp.net"
	if err := s.InsertRaw(ctx,
		chat, chat, "MSG-PLAIN-1", 1_700_000_001,
		"hello", "", "", "Cliente", false, nil, "", "",
	); err != nil {
		t.Fatalf("InsertRaw: %v", err)
	}

	got, err := s.QueryHistory(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryHistory returned %d rows, want 1", len(got))
	}
	if len(got[0].InteractiveJSON) != 0 {
		t.Errorf("interactive_json = %q, want NULL/empty for ordinary message", got[0].InteractiveJSON)
	}
	decoded, err := sqlitehistory.UnmarshalInteractive(got[0].InteractiveJSON)
	if err != nil {
		t.Fatalf("UnmarshalInteractive: %v", err)
	}
	if decoded != nil {
		t.Errorf("decoded = %+v, want nil for ordinary message", decoded)
	}
}

// TestMarshalUnmarshalInteractive covers the encode/decode helpers directly,
// including the nil and malformed paths (issue #201).
func TestMarshalUnmarshalInteractive(t *testing.T) {
	t.Parallel()

	// nil payload → nil bytes → nil payload.
	b, err := sqlitehistory.MarshalInteractive(nil)
	if err != nil {
		t.Fatalf("MarshalInteractive(nil): %v", err)
	}
	if b != nil {
		t.Errorf("MarshalInteractive(nil) = %q, want nil", b)
	}
	p, err := sqlitehistory.UnmarshalInteractive(nil)
	if err != nil || p != nil {
		t.Errorf("UnmarshalInteractive(nil) = (%+v, %v), want (nil, nil)", p, err)
	}

	// buttons subtype round-trip preserves the lowercase wire string.
	in := &domain.InteractivePayload{
		Subtype: domain.InteractiveButtons,
		Prompt:  "Yes or no?",
		Options: []domain.InteractiveOption{{ID: "btn_yes", Label: "Yes"}},
	}
	b, err = sqlitehistory.MarshalInteractive(in)
	if err != nil {
		t.Fatalf("MarshalInteractive: %v", err)
	}
	out, err := sqlitehistory.UnmarshalInteractive(b)
	if err != nil {
		t.Fatalf("UnmarshalInteractive: %v", err)
	}
	if out.Subtype != domain.InteractiveButtons || out.Prompt != "Yes or no?" ||
		len(out.Options) != 1 || out.Options[0].ID != "btn_yes" || out.Options[0].Label != "Yes" {
		t.Errorf("round-trip mismatch: got %+v", out)
	}

	// Malformed BLOB → error, never a panic.
	if _, err := sqlitehistory.UnmarshalInteractive([]byte(`{"subtype":`)); err == nil {
		t.Error("UnmarshalInteractive(malformed) = nil error, want error")
	}
}
