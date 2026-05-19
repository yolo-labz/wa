package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeProbe is a tiny WebsocketProbe stub for the stale-matrix table. It
// holds a single bool; tests flip it before each iteration. Using a
// concrete struct (not a closure) keeps the table rows declarative.
type fakeProbe struct{ up bool }

func (p *fakeProbe) WebsocketConnected() bool { return p.up }

// TestHealth_StaleMatrix exercises every (websocket × threshold × stale)
// combination handleHealth distinguishes per spec 110g FR-005. Each row
// asserts the wire shape an operator sees on `wa health --json`. White-
// box test (`package app`) so it can poke bridge.lastEventUnix directly
// — no real clock skew, no flaky sleeps.
func TestHealth_StaleMatrix(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()

	cases := []struct {
		name          string
		wsUp          bool
		threshold     int
		lastEventTs   int64
		wantConnected bool
		wantStaleSec  int64
		wantState     string
	}{
		{
			name:          "ws_up_below_threshold_healthy",
			wsUp:          true,
			threshold:     300,
			lastEventTs:   now - 30, // 30s stale, well under 300s
			wantConnected: true,
			wantStaleSec:  30,
			wantState:     "healthy",
		},
		{
			name:          "ws_up_at_threshold_stale",
			wsUp:          true,
			threshold:     60,
			lastEventTs:   now - 60, // exactly threshold -> stale
			wantConnected: true,
			wantStaleSec:  60,
			wantState:     "stale",
		},
		{
			name:          "ws_up_well_past_threshold_stale",
			wsUp:          true,
			threshold:     300,
			lastEventTs:   now - 1800, // 30min, six thresholds in
			wantConnected: true,
			wantStaleSec:  1800,
			wantState:     "stale",
		},
		{
			name:          "ws_down_unknown",
			wsUp:          false,
			threshold:     300,
			lastEventTs:   now - 30,
			wantConnected: false,
			wantStaleSec:  30,
			wantState:     "unknown",
		},
		{
			name:          "threshold_disabled_unknown",
			wsUp:          true,
			threshold:     0,
			lastEventTs:   now - 30,
			wantConnected: true,
			wantStaleSec:  30,
			wantState:     "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter := memory.New(nil)
			// Save a non-zero session so paired=true; CreatedAt is the
			// 30-day-old timestamp we hand the dispatcher below.
			sess, err := domain.NewSession(
				domain.MustJID("12025550100@s.whatsapp.net"),
				1,
				time.Now().Add(-30*24*time.Hour),
			)
			if err != nil {
				t.Fatalf("new session: %v", err)
			}
			if err := adapter.Save(context.Background(), sess); err != nil {
				t.Fatalf("session save: %v", err)
			}

			probe := &fakeProbe{up: tc.wsUp}
			cfg := app.DispatcherConfig{
				Sender:                adapter,
				Events:                adapter,
				Contacts:              adapter,
				Groups:                adapter,
				Session:               adapter,
				Allowlist:             adapter,
				Audit:                 adapter,
				History:               adapter,
				Pairer:                adapter,
				SessionCreated:        time.Now().Add(-30 * 24 * time.Hour),
				Profile:               "stale-matrix",
				Websocket:             probe,
				SoftStaleThresholdSec: tc.threshold,
			}
			d := app.NewDispatcher(cfg)
			t.Cleanup(func() { _ = d.Close() })

			// Directly seed lastEventUnix — handleHealth reads
			// time.Now().Unix() minus this value, so the matrix is
			// deterministic without clock injection.
			d.Bridge().SetLastEventUnixForTest(tc.lastEventTs)

			raw, err := d.Handle(context.Background(), "health", nil)
			if err != nil {
				t.Fatalf("health: %v", err)
			}

			var h struct {
				Schema                string `json:"schema"`
				Connected             bool   `json:"connected"`
				StaleSeconds          int64  `json:"staleSeconds"`
				StaleState            string `json:"staleState"`
				SoftStaleThresholdSec int    `json:"softStaleThresholdSec"`
			}
			if err := json.Unmarshal(raw, &h); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if h.Connected != tc.wantConnected {
				t.Errorf("connected = %v, want %v", h.Connected, tc.wantConnected)
			}
			// staleSeconds is computed from a live time.Now() so it can
			// drift by a second between the test's `now` capture and the
			// dispatcher's read. Allow +/- 2s slop — the matrix is about
			// classification, not microsecond accuracy.
			diff := h.StaleSeconds - tc.wantStaleSec
			if diff < -2 || diff > 2 {
				t.Errorf("staleSeconds = %d, want ~%d (+/-2)", h.StaleSeconds, tc.wantStaleSec)
			}
			if h.StaleState != tc.wantState {
				t.Errorf("staleState = %q, want %q", h.StaleState, tc.wantState)
			}
		})
	}
}

// TestHealth_NoEventsYet asserts that with no event ever observed, the
// stale fields are omitted entirely — the daemon refuses to synthesise
// staleSeconds=0 from a zero clock (would lie about freshness). FR-005.
func TestHealth_NoEventsYet(t *testing.T) {
	t.Parallel()

	adapter := memory.New(nil)
	probe := &fakeProbe{up: true}
	cfg := app.DispatcherConfig{
		Sender: adapter, Events: adapter, Contacts: adapter,
		Groups: adapter, Session: adapter, Allowlist: adapter,
		Audit: adapter, History: adapter, Pairer: adapter,
		Profile:               "no-events",
		Websocket:             probe,
		SoftStaleThresholdSec: 300,
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })

	raw, err := d.Handle(context.Background(), "health", nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	// Decode into a flexible map so we can assert *omission*, not just
	// zero values — the wire contract is that the fields are absent.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["staleSeconds"]; ok {
		t.Errorf("staleSeconds present with no events observed; got %v", m["staleSeconds"])
	}
	if _, ok := m["staleState"]; ok {
		t.Errorf("staleState present with no events observed; got %v", m["staleState"])
	}
}

// TestHealth_ProbeMissing_FallsBackToPaired covers the pre-110g
// composition root path where Websocket is nil. Connected must echo
// paired, and stale fields must be omitted (no probe -> nothing to
// classify against).
func TestHealth_ProbeMissing_FallsBackToPaired(t *testing.T) {
	t.Parallel()

	adapter := memory.New(nil)
	sess, err := domain.NewSession(
		domain.MustJID("12025550100@s.whatsapp.net"),
		1,
		time.Now().Add(-30*24*time.Hour),
	)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := adapter.Save(context.Background(), sess); err != nil {
		t.Fatalf("session save: %v", err)
	}

	cfg := app.DispatcherConfig{
		Sender: adapter, Events: adapter, Contacts: adapter,
		Groups: adapter, Session: adapter, Allowlist: adapter,
		Audit: adapter, History: adapter, Pairer: adapter,
		Profile: "no-probe",
		// Websocket intentionally nil.
		SoftStaleThresholdSec: 300,
	}
	d := app.NewDispatcher(cfg)
	t.Cleanup(func() { _ = d.Close() })

	d.Bridge().SetLastEventUnixForTest(time.Now().Unix() - 30)

	raw, err := d.Handle(context.Background(), "health", nil)
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if conn, _ := m["connected"].(bool); !conn {
		t.Errorf("connected = false, want true (paired fallback)")
	}
	if _, ok := m["staleState"]; ok {
		t.Errorf("staleState present without probe; got %v", m["staleState"])
	}
}
