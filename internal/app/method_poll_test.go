package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakePollManager implements app.PollManager. The v2.0.0 wire contract is
// "every well-formed call returns -32000 upstream_error" — so the fake
// returns domain.ErrUpstreamError to mirror the whatsmeow adapter exactly.
type fakePollManager struct {
	calls int
}

func (f *fakePollManager) Create(_ context.Context, chat domain.JID, question string, options []string, _ int) (domain.MessageID, error) {
	if chat.IsZero() {
		return "", domain.ErrInvalidJID
	}
	if question == "" || len(options) < 2 {
		return "", domain.ErrUpstreamError
	}
	f.calls++
	return domain.MessageID("fake-poll-1"), nil
}

func (f *fakePollManager) Vote(_ context.Context, chat domain.JID, pollID domain.MessageID, _ []int) error {
	if chat.IsZero() || pollID == "" {
		return domain.ErrInvalidJID
	}
	f.calls++
	return domain.ErrUpstreamError
}

func newPollDispatcher(t *testing.T, pm app.PollManager) (*app.Dispatcher, *memory.Adapter) {
	t.Helper()
	adapter := memory.New(nil)
	cfg := app.DispatcherConfig{
		Sender:         adapter,
		Events:         adapter,
		Contacts:       adapter,
		Groups:         adapter,
		Session:        adapter,
		Allowlist:      adapter,
		Audit:          adapter,
		History:        adapter,
		Pairer:         adapter,
		Polls:          pm,
		SessionCreated: time.Now().Add(-30 * 24 * time.Hour),
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })
	return d, adapter
}

// TestPollVoteReturnsUpstreamError verifies that a well-formed poll.vote is
// routed through the adapter and surfaces domain.ErrUpstreamError, which the
// socket layer maps to -32000 on the wire (v2.0.0 contract PM1..PM4).
func TestPollVoteReturnsUpstreamError(t *testing.T) {
	pm := &fakePollManager{}
	d, _ := newPollDispatcher(t, pm)

	params, _ := json.Marshal(map[string]any{
		"chat":     testJIDStr,
		"pollId":   "3EB0abc123",
		"selected": []int{0, 2},
	})
	_, err := d.Handle(context.Background(), "poll.vote", params)
	if !errors.Is(err, domain.ErrUpstreamError) {
		t.Fatalf("poll.vote err = %v, want ErrUpstreamError", err)
	}
	if pm.calls != 1 {
		t.Errorf("want 1 adapter call, got %d", pm.calls)
	}
}

// TestPollVoteAbsentReturnsMethodNotFound verifies graceful degradation when
// the PollManager port is nil — poll.vote returns ErrMethodNotFound.
func TestPollVoteAbsentReturnsMethodNotFound(t *testing.T) {
	d, _ := newPollDispatcher(t, nil)

	params, _ := json.Marshal(map[string]any{
		"chat":     testJIDStr,
		"pollId":   "3EB0abc123",
		"selected": []int{0},
	})
	_, err := d.Handle(context.Background(), "poll.vote", params)
	if !errors.Is(err, app.ErrMethodNotFound) {
		t.Fatalf("poll.vote without Polls = %v, want ErrMethodNotFound", err)
	}
}

// TestPollVoteRejectsEmptyFields verifies shape validation precedes upstream
// dispatch — empty chat or pollId yields ErrInvalidParams with zero adapter
// calls, so the wire code stays -32602 not -32000.
func TestPollVoteRejectsEmptyFields(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
	}{
		{"missing chat", map[string]any{"pollId": "X", "selected": []int{0}}},
		{"missing pollId", map[string]any{"chat": testJIDStr, "selected": []int{0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := &fakePollManager{}
			d, _ := newPollDispatcher(t, pm)

			params, _ := json.Marshal(tc.params)
			_, err := d.Handle(context.Background(), "poll.vote", params)
			if !errors.Is(err, app.ErrInvalidParams) {
				t.Fatalf("err = %v, want ErrInvalidParams", err)
			}
			if pm.calls != 0 {
				t.Errorf("want 0 adapter calls, got %d", pm.calls)
			}
		})
	}
}

// --- poll.create (F-091) ---

// TestPollCreateSendsAndReturnsID: the happy path reaches the port with the
// question and options intact, and the wire result carries the message id.
func TestPollCreateSendsAndReturnsID(t *testing.T) {
	pm := memory.NewPollManager()
	d, _ := newPollDispatcher(t, pm)

	params, _ := json.Marshal(map[string]any{
		"chat":       testJIDStr,
		"question":   "lunch?",
		"options":    []string{"pizza", "sushi"},
		"selectable": 1,
	})
	raw, err := d.Handle(context.Background(), "poll.create", params)
	if err != nil {
		t.Fatalf("poll.create: %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("empty messageId in result")
	}
	created := pm.Created()
	if len(created) != 1 {
		t.Fatalf("want 1 recorded poll, got %d", len(created))
	}
	if created[0].Question != "lunch?" {
		t.Errorf("question = %q, want %q", created[0].Question, "lunch?")
	}
	if len(created[0].Options) != 2 || created[0].Options[0] != "pizza" {
		t.Errorf("options = %v, want [pizza sushi]", created[0].Options)
	}
	if created[0].Selectable != 1 {
		t.Errorf("selectable = %d, want 1", created[0].Selectable)
	}
}

// TestPollCreateRejectsBadShapes: every shape WhatsApp itself refuses must
// cost no round trip — the port is never reached.
func TestPollCreateRejectsBadShapes(t *testing.T) {
	longOpt := strings.Repeat("x", domain.MaxTextBytes+1)
	cases := map[string]map[string]any{
		"no options":        {"chat": testJIDStr, "question": "q"},
		"one option":        {"chat": testJIDStr, "question": "q", "options": []string{"only"}},
		"thirteen options":  {"chat": testJIDStr, "question": "q", "options": make13()},
		"blank option":      {"chat": testJIDStr, "question": "q", "options": []string{"a", ""}},
		"duplicate options": {"chat": testJIDStr, "question": "q", "options": []string{"a", "a"}},
		"empty question":    {"chat": testJIDStr, "question": "", "options": []string{"a", "b"}},
		"oversize option":   {"chat": testJIDStr, "question": "q", "options": []string{"a", longOpt}},
		"selectable > opts": {"chat": testJIDStr, "question": "q", "options": []string{"a", "b"}, "selectable": 3},
		"negative select":   {"chat": testJIDStr, "question": "q", "options": []string{"a", "b"}, "selectable": -1},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pm := memory.NewPollManager()
			d, _ := newPollDispatcher(t, pm)
			params, _ := json.Marshal(body)
			if _, err := d.Handle(context.Background(), "poll.create", params); err == nil {
				t.Fatal("expected an error, got nil")
			}
			if got := len(pm.Created()); got != 0 {
				t.Errorf("port reached %d times, want 0", got)
			}
		})
	}
}

// TestPollCreateRejectsBadJID: a syntactically invalid JID is an invalid-JID
// error, not a send.
func TestPollCreateRejectsBadJID(t *testing.T) {
	pm := memory.NewPollManager()
	d, _ := newPollDispatcher(t, pm)
	params, _ := json.Marshal(map[string]any{
		"chat": "not-a-jid", "question": "q", "options": []string{"a", "b"},
	})
	if _, err := d.Handle(context.Background(), "poll.create", params); err == nil {
		t.Fatal("expected an error for a malformed JID")
	}
	if got := len(pm.Created()); got != 0 {
		t.Errorf("port reached %d times, want 0", got)
	}
}

// TestPollCreateMethodNotFoundWithoutPort: no PollManager wired means the
// method is absent rather than silently succeeding.
func TestPollCreateMethodNotFoundWithoutPort(t *testing.T) {
	d, _ := newPollDispatcher(t, nil)
	params, _ := json.Marshal(map[string]any{
		"chat": testJIDStr, "question": "q", "options": []string{"a", "b"},
	})
	if _, err := d.Handle(context.Background(), "poll.create", params); err == nil {
		t.Fatal("expected method-not-found, got nil")
	}
}

func make13() []string {
	out := make([]string, 13)
	for i := range out {
		out[i] = string(rune('a' + i))
	}
	return out
}
