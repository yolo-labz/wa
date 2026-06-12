package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// LatestIncomingHarness couples the LatestIncomingFinder port with the
// seeding hooks the contract clauses need. Direction matters here —
// unlike HistoryStoreHarness.AppendHistory, the clauses must place both
// incoming and outgoing rows to prove the from-me filter.
type LatestIncomingHarness interface {
	app.LatestIncomingFinder
	// SeedMessage records one message for chat with the given id and
	// ascending insertion order across calls; fromMe marks direction.
	SeedMessage(chat domain.JID, id domain.MessageID, fromMe bool)
}

// LatestIncomingFactory returns a fresh harness for one sub-test.
type LatestIncomingFactory func(t *testing.T) LatestIncomingHarness

// RunLatestIncomingFinderContract exercises the LatestIncomingFinder
// clauses (sendSeen anchor lookup). Standalone runner per the
// registry.go convention.
func RunLatestIncomingFinderContract(t *testing.T, factory LatestIncomingFactory) {
	t.Helper()

	chat := domain.MustJID("5511999999999")
	other := domain.MustJID("5511888888888")

	t.Run("LI1_newest_incoming_wins", func(t *testing.T) {
		a := factory(t)
		a.SeedMessage(chat, "IN-1", false)
		a.SeedMessage(chat, "OUT-1", true)
		a.SeedMessage(chat, "IN-2", false)
		a.SeedMessage(chat, "OUT-2", true) // newer than IN-2 but from-me

		id, ok, err := a.LatestIncoming(context.Background(), chat)
		if err != nil {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI1", "nil error", err.Error())
		}
		if !ok {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI1", "ok=true", "ok=false")
		}
		if id != "IN-2" {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI1", "IN-2 (newest incoming, not newest overall)", string(id))
		}
	})

	t.Run("LI2_outgoing_only_not_found", func(t *testing.T) {
		a := factory(t)
		a.SeedMessage(chat, "OUT-1", true)
		a.SeedMessage(chat, "OUT-2", true)

		_, ok, err := a.LatestIncoming(context.Background(), chat)
		if err != nil {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI2", "nil error", err.Error())
		}
		if ok {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI2", "ok=false for from-me-only chat", "ok=true")
		}
	})

	t.Run("LI3_unknown_chat_not_found", func(t *testing.T) {
		a := factory(t)
		a.SeedMessage(chat, "IN-1", false)

		_, ok, err := a.LatestIncoming(context.Background(), other)
		if err != nil {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI3", "nil error", err.Error())
		}
		if ok {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI3", "ok=false for unseeded chat", "ok=true")
		}
	})

	t.Run("LI4_zero_jid_rejected", func(t *testing.T) {
		a := factory(t)
		_, _, err := a.LatestIncoming(context.Background(), domain.JID{})
		if err == nil {
			reportf(t, "LatestIncomingFinder", "LatestIncoming", "LI4", "error for zero JID", "nil")
		}
	})
}
