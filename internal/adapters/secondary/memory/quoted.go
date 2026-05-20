package memory

import (
	"context"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// quotedStore is the in-memory backing of the app.QuotedMessageStore
// implementation provided by the memory adapter (#163). Test fixtures
// seed the map via SeedQuotedRaw; the dispatcher fetches via
// GetRawProto on the same adapter instance.
type quotedStore struct {
	mu      sync.RWMutex
	byMsgID map[domain.MessageID][]byte
}

func newQuotedStore() *quotedStore {
	return &quotedStore{byMsgID: make(map[domain.MessageID][]byte)}
}

// SeedQuotedRaw inserts a raw_proto blob keyed by messageID. Test
// fixtures call this before invoking send.listResponse / send.buttonResponse
// so the dispatcher's QuotedMessageStore lookup hits. Production never
// uses this path — sqlitehistory.QuotedMessageAdapter is the real impl.
func (a *Adapter) SeedQuotedRaw(messageID domain.MessageID, rawProto []byte) {
	a.quoted.mu.Lock()
	defer a.quoted.mu.Unlock()
	a.quoted.byMsgID[messageID] = rawProto
}

// GetRawProto implements app.QuotedMessageStore. Returns
// app.ErrMessageNotFound when the messageID was not seeded.
func (a *Adapter) GetRawProto(_ context.Context, messageID domain.MessageID) ([]byte, error) {
	a.quoted.mu.RLock()
	defer a.quoted.mu.RUnlock()
	raw, ok := a.quoted.byMsgID[messageID]
	if !ok {
		return nil, app.ErrMessageNotFound
	}
	return raw, nil
}
