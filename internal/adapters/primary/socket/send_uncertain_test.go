package socket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

type uncertainDispatcher struct{ okDispatcher }

func (uncertainDispatcher) Handle(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, domain.NewSendUncertain("WAID-3", errors.New("ack timeout"))
}

// The socket answers a possibly-delivered send with -32023 and the id in
// error.data.message_id, so a CLI or SDK caller can look it up.
func TestDispatch_SendUncertainCarriesTheMessageID(t *testing.T) {
	s := NewServer(uncertainDispatcher{}, slog.New(slog.DiscardHandler))
	_, err := s.dispatchRecovered(context.Background(), "send", "1", nil)
	var jerr *jrpc2.Error
	if !errors.As(err, &jerr) || int(jerr.Code) != -32023 {
		t.Fatalf("err %v, want -32023", err)
	}
	var data domain.ErrorData
	if json.Unmarshal(jerr.Data, &data) != nil || data.MessageID != "WAID-3" || data.Reason != domain.ReasonSendUncertain {
		t.Fatalf("data %+v, want send_uncertain with message_id WAID-3", data)
	}
}
