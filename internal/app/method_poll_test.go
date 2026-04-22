package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// fakePollManager implements app.PollManager. The v2.0.0 wire contract is
// "every well-formed call returns -32000 upstream_error" — so the fake
// returns domain.ErrUpstreamError to mirror the whatsmeow adapter exactly.
type fakePollManager struct {
	calls int
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
