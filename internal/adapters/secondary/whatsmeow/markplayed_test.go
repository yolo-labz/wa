package whatsmeow

import (
	"context"
	"strings"
	"testing"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestReceiptTypeExtraByMethod pins the one thing that separates the two
// receipt methods on the wire: MarkRead sends no extra receipt type,
// MarkPlayed sends exactly one, and it is ReceiptTypePlayed.
//
// The length assertion is load-bearing, not decorative. whatsmeow's
// Client.MarkRead documents "providing more than one receipt type will
// panic: the parameter is only a vararg for backwards compatibility", so
// a change that appends instead of replacing would take the daemon down
// rather than return an error.
func TestReceiptTypeExtraByMethod(t *testing.T) {
	dm := mustParseJIDT("5511911111111@s.whatsapp.net")

	tests := []struct {
		name string
		call func(*Adapter, context.Context) error
		want []waTypes.ReceiptType
	}{
		{
			name: "MarkRead sends no extra type",
			call: func(a *Adapter, ctx context.Context) error { return a.MarkRead(ctx, dm, "MSG-R") },
			want: nil,
		},
		{
			name: "MarkPlayed sends exactly one played type",
			call: func(a *Adapter, ctx context.Context) error { return a.MarkPlayed(ctx, dm, "MSG-P") },
			want: []waTypes.ReceiptType{waTypes.ReceiptTypePlayed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := newFakeClient()
			fc.ConnectedFlag = true
			a := newTestAdapter(t, fc)
			a.history = &stubHistory{senders: map[string]string{}}

			if err := tt.call(a, context.Background()); err != nil {
				t.Fatalf("receipt call: %v", err)
			}
			if got := len(fc.MarkReadCalls); got != 1 {
				t.Fatalf("want 1 client call, got %d", got)
			}
			got := fc.MarkReadCalls[0].Extra
			if len(got) != len(tt.want) {
				t.Fatalf("receipt types: want %v (%d), got %v (%d)", tt.want, len(tt.want), got, len(got))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("receipt type %d: want %q, got %q", i, tt.want[i], got[i])
				}
			}
		})
	}
}

// TestMarkPlayedResolvesGroupSender guards against MarkPlayed becoming a
// second, divergent copy of the receipt path. Group receipts need the
// authoring participant JID, not the group JID (R-09); sharing one body
// with MarkRead is what keeps that true for both.
func TestMarkPlayedResolvesGroupSender(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)

	member := "5511922222222@s.whatsapp.net"
	group := mustParseJIDT("120363000000000000@g.us")
	a.history = &stubHistory{senders: map[string]string{"MSG-G": member}}

	if err := a.MarkPlayed(context.Background(), group, "MSG-G"); err != nil {
		t.Fatalf("MarkPlayed group: %v", err)
	}
	if got := len(fc.MarkReadCalls); got != 1 {
		t.Fatalf("want 1 client call, got %d", got)
	}
	call := fc.MarkReadCalls[0]
	if call.Sender.String() != member {
		t.Errorf("sender: want %s, got %s", member, call.Sender.String())
	}
	if call.Sender.String() == call.Chat.String() {
		t.Errorf("sender must differ from chat in group receipts")
	}
}

// TestMarkPlayedErrorsNameTheirMethod: a caller that asked for a played
// receipt must not read "MarkRead" in the failure. The two methods share
// a body, so the method name has to be threaded through rather than
// hardcoded in the error prefix.
func TestMarkPlayedErrorsNameTheirMethod(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)

	err := a.MarkPlayed(context.Background(), domain.JID{}, "MSG-X")
	if err == nil {
		t.Fatal("want error for zero JID; got nil")
	}
	if !strings.Contains(err.Error(), "MessageSender.MarkPlayed") {
		t.Errorf("want MarkPlayed in error prefix, got: %v", err)
	}
	if len(fc.MarkReadCalls) != 0 {
		t.Errorf("must not invoke the client on a validation failure")
	}
}
