package whatsmeow

import (
	"strings"
	"testing"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/internal/domain"
)

func TestToDomain_User(t *testing.T) {
	t.Parallel()
	wa, err := waTypes.ParseJID("5511999990000@s.whatsapp.net")
	if err != nil {
		t.Fatalf("whatsmeow ParseJID: %v", err)
	}
	got, err := toDomain(wa)
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}
	if !got.IsUser() {
		t.Errorf("expected user JID, got %v", got)
	}
	if got.String() != "5511999990000@s.whatsapp.net" {
		t.Errorf("round-trip mismatch: %q", got.String())
	}
}

func TestToDomain_Group(t *testing.T) {
	t.Parallel()
	wa, err := waTypes.ParseJID("120363000000000000@g.us")
	if err != nil {
		t.Fatalf("whatsmeow ParseJID: %v", err)
	}
	got, err := toDomain(wa)
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}
	if !got.IsGroup() {
		t.Errorf("expected group JID, got %v", got)
	}
}

func TestToDomain_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		"5511999990000@s.whatsapp.net",
		"14155550123@s.whatsapp.net",
		"120363000000000000@g.us",
	}
	for _, in := range cases {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			wa, err := waTypes.ParseJID(in)
			if err != nil {
				t.Fatalf("whatsmeow ParseJID: %v", err)
			}
			d, err := toDomain(wa)
			if err != nil {
				t.Fatalf("toDomain: %v", err)
			}
			back := toWhatsmeow(d)
			if back.String() != wa.String() {
				t.Errorf("round-trip: got %q want %q", back.String(), wa.String())
			}
		})
	}
}

func TestToWhatsmeow_PanicsOnZero(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on zero JID, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "zero JID") {
			t.Errorf("panic message = %v, want contain %q", r, "zero JID")
		}
	}()
	_ = toWhatsmeow(domain.JID{})
}

func TestToWhatsmeow_UserJID(t *testing.T) {
	t.Parallel()
	d := domain.MustJID("5511999990000@s.whatsapp.net")
	wa := toWhatsmeow(d)
	if wa.String() != "5511999990000@s.whatsapp.net" {
		t.Errorf("got %q", wa.String())
	}
}

func TestToWhatsmeow_GroupJID(t *testing.T) {
	t.Parallel()
	d := domain.MustJID("120363000000000000@g.us")
	wa := toWhatsmeow(d)
	if wa.String() != "120363000000000000@g.us" {
		t.Errorf("got %q", wa.String())
	}
}

func TestToWhatsmeow_FromParsePhone(t *testing.T) {
	t.Parallel()
	d, err := domain.ParsePhone("+5511999990000")
	if err != nil {
		t.Fatalf("ParsePhone: %v", err)
	}
	wa := toWhatsmeow(d)
	if wa.String() != "5511999990000@s.whatsapp.net" {
		t.Errorf("got %q", wa.String())
	}
}

func TestToDomain_InvalidString(t *testing.T) {
	t.Parallel()
	// Construct a whatsmeow JID with an unknown server. Its string form
	// will fail domain.Parse and toDomain should surface the error.
	wa := waTypes.JID{User: "123", Server: "broadcast"}
	if _, err := toDomain(wa); err == nil {
		t.Error("expected error on unknown server, got nil")
	}
}
