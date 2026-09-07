package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// The real 17/08 case: the published thirteen-digit form is registered, but
// the server routes it under the twelve-digit one.
const (
	publishedJID = "5581987200047@s.whatsapp.net"
	canonicalJID = "558187200047@s.whatsapp.net"
)

// movedChecker answers every query with the canonical JID, standing in for
// the usync response that started issue #354.
type movedChecker struct{ canonical domain.JID }

func (m movedChecker) IsOnWhatsApp(_ context.Context, _ string) (domain.JID, bool, error) {
	return m.canonical, true, nil
}

// followDispatcher wires a real allowlist (memory.Adapter) behind the real
// safety pipeline, so the allowlist assertions below exercise the control
// itself rather than a stub of it.
func followDispatcher(t *testing.T, canonical domain.JID, grant ...string) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	for _, g := range grant {
		adapter.Grant(domain.MustJID(g), domain.ActionSend)
	}
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: adapter, Events: adapter, Contacts: adapter, Groups: adapter,
		Session: adapter, Allowlist: adapter, Audit: adapter, History: adapter,
		Pairer: adapter, Quoted: adapter,
		OnWhatsApp:     movedChecker{canonical: canonical},
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	})
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

func sendParams(t *testing.T, to string, follow bool) json.RawMessage {
	t.Helper()
	p := map[string]any{"to": to, "body": "oi"}
	if follow {
		p["followCanonical"] = true
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// TestFollowCanonicalRetargetsAndReports — the opt-in itself, end to end
// through Handle. Both JIDs allowlisted, so the send lands on the canonical
// one and says so.
func TestFollowCanonicalRetargetsAndReports(t *testing.T) {
	d, _ := followDispatcher(t, domain.MustJID(canonicalJID), publishedJID, canonicalJID)

	raw, err := d.Handle(context.Background(), "send", sendParams(t, publishedJID, true))
	if err != nil {
		t.Fatalf("Handle(send, followCanonical): %v", err)
	}
	var got struct {
		MessageID  string `json:"messageId"`
		ResolvedTo string `json:"resolvedTo"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.MessageID == "" {
		t.Error("no messageId — the send did not happen")
	}
	if got.ResolvedTo != canonicalJID {
		t.Errorf("resolvedTo = %q, want %q", got.ResolvedTo, canonicalJID)
	}
}

// TestFollowCanonicalOffByDefault — without the opt-in the -32020 refusal
// stands. #357 chose this default deliberately: a send cannot be recalled,
// so the daemon must not pick a recipient the caller did not name.
func TestFollowCanonicalOffByDefault(t *testing.T) {
	d, _ := followDispatcher(t, domain.MustJID(canonicalJID), publishedJID, canonicalJID)

	_, err := d.Handle(context.Background(), "send", sendParams(t, publishedJID, false))
	if !errors.Is(err, app.ErrRecipientMoved) {
		t.Fatalf("want ErrRecipientMoved without the opt-in, got %v", err)
	}
}

// TestFollowCanonicalStillNeedsAllowlist is the load-bearing test.
//
// Following retargets a message to a JID the caller never named. If that
// skipped the allowlist, the opt-in would launder an unapproved recipient
// past the one control that exists to stop it — worse than the -32603 this
// issue started from. Opting in widens who the CALLER will reach; it must
// never widen who the DAEMON will message.
func TestFollowCanonicalStillNeedsAllowlist(t *testing.T) {
	// Published is granted; the canonical it resolves to is NOT.
	d, _ := followDispatcher(t, domain.MustJID(canonicalJID), publishedJID)

	_, err := d.Handle(context.Background(), "send", sendParams(t, publishedJID, true))
	if err == nil {
		t.Fatal("followCanonical sent to a JID that was never allowlisted")
	}
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("want ErrNotAllowlisted for an un-allowlisted canonical, got %v", err)
	}
}

// TestFollowCanonicalIgnoresOtherRefusals — the opt-in is scoped to -32020.
// A plain allowlist denial on the requested JID is returned as itself, not
// "followed" anywhere.
func TestFollowCanonicalIgnoresOtherRefusals(t *testing.T) {
	d, _ := followDispatcher(t, domain.MustJID(canonicalJID)) // nothing granted

	_, err := d.Handle(context.Background(), "send", sendParams(t, publishedJID, true))
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("want the original ErrNotAllowlisted, got %v", err)
	}
}

// TestFollowCanonicalNoOpWhenNothingMoved — an ordinary send is untouched
// even with the flag on, and omits resolvedTo, so a caller can treat the
// field's presence as a reliable "this went somewhere else" signal.
func TestFollowCanonicalNoOpWhenNothingMoved(t *testing.T) {
	ordinary := "5511999999999@s.whatsapp.net"
	// The server echoes the queried number back — the common case.
	d, _ := followDispatcher(t, domain.MustJID(ordinary), ordinary)

	raw, err := d.Handle(context.Background(), "send", sendParams(t, ordinary, true))
	if err != nil {
		t.Fatalf("Handle(send): %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := got["resolvedTo"]; present {
		t.Errorf("resolvedTo must be omitted on an ordinary send: %s", raw)
	}
}
