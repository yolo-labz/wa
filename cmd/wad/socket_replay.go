package main

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/socket"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
)

// socketReplayShim adapts *sqliteevents.Store → socket.EventReplayer so the
// FR-061 `subscribe({since})` resume can read the durable ring without the
// socket package importing the concrete adapter. It is the peer of
// eventsReplayShim (rest.EventReplayer) over the same store — the two
// transports replay the same rows, so a client that reads a seq off the
// socket and resumes over SSE lands on the same row and vice versa.
//
// The projections differ in one respect: SSE emits whole rows, so it needs no
// selectors, while the socket evaluates the FR-060 filter DSL server-side and
// therefore rehydrates the chat/sender/body the pump stored in untrusted_json.
type socketReplayShim struct {
	store *sqliteevents.Store
}

func (s *socketReplayShim) OldestSeq(ctx context.Context) (int64, error) {
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return 0, err
	}
	return stats.OldestSeq, nil
}

func (s *socketReplayShim) Replay(ctx context.Context, sinceSeq int64, limit int) ([]socket.ReplayRecord, error) {
	recs, err := s.store.Range(ctx, sinceSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]socket.ReplayRecord, 0, len(recs))
	for _, rec := range recs {
		// A row written before this shim existed — or by a pump that
		// failed to marshal its selectors — has an empty untrusted_json.
		// Decoding it yields the zero selectors, which makes a filtered
		// subscription skip the row rather than deliver it unfiltered.
		// That is the safe direction: a filter is a request not to see
		// something, and honouring it too strictly loses an event the
		// gap detector will report, while honouring it too loosely
		// delivers one the client explicitly excluded.
		var sel eventSelectors
		if len(rec.UntrustedJSON) > 0 {
			_ = json.Unmarshal(rec.UntrustedJSON, &sel)
		}
		out = append(out, socket.ReplayRecord{
			Seq:    rec.Seq,
			Kind:   rec.Kind,
			Data:   rec.TrustedJSON,
			Chat:   sel.Chat,
			Sender: sel.Sender,
			Body:   sel.Body,
		})
	}
	return out, nil
}
