package sqlitehistory

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// QuotedMessageAdapter wraps a *Store with the app.QuotedMessageStore
// surface used by the dispatcher to hydrate ContextInfo.QuotedMessage
// on outbound list/button replies (#163).
//
// The adapter is a thin shim: it reuses Store.GetRawProto and translates
// the os.ErrNotExist wrap into app.ErrMessageNotFound so the dispatcher
// can convert to a typed RPC error without depending on sqlite/os
// internals. Keeping the shim in the sqlitehistory package keeps the app
// layer free of sqlite imports.
type QuotedMessageAdapter struct {
	Store *Store
}

// NewQuotedMessageAdapter constructs an adapter bound to the given Store.
// Production composition wires a non-nil store eagerly; a nil store is
// surfaced as an error at first GetRawProto call rather than at
// construction (avoids package-init panic, satisfies forbidigo).
func NewQuotedMessageAdapter(store *Store) *QuotedMessageAdapter {
	return &QuotedMessageAdapter{Store: store}
}

// GetRawProto implements app.QuotedMessageStore. Returns
// app.ErrMessageNotFound when the message ID is unknown so the
// dispatcher can translate to ErrInvalidParams (-32602) with an
// operator-facing message. All other errors are wrapped verbatim and
// surface as -32603 internal-error.
func (a *QuotedMessageAdapter) GetRawProto(ctx context.Context, messageID domain.MessageID) ([]byte, error) {
	if a.Store == nil {
		return nil, errors.New("sqlitehistory.QuotedMessageAdapter: nil Store")
	}
	if messageID == "" {
		return nil, app.ErrMessageNotFound
	}
	_, rawProto, err := a.Store.GetRawProto(ctx, string(messageID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, app.ErrMessageNotFound
		}
		return nil, fmt.Errorf("sqlitehistory.QuotedMessageAdapter.GetRawProto: %w", err)
	}
	if len(rawProto) == 0 {
		// Historical row from before raw_proto persistence (pre v1.2.1).
		// Caller treats this as "no quote available"; the WhatsApp wire
		// will still reject because QuotedMessage stays nil — surface a
		// not-found instead of a silent corruption.
		return nil, app.ErrMessageNotFound
	}
	return rawProto, nil
}
