package rest

import (
	"context"
	"encoding/json"
)

// Dispatcher is the same contract the socket primary adapter uses
// (internal/adapters/primary/socket/dispatcher.go). Spec 110a defines
// it locally so the rest adapter does not import the socket package
// — both adapters depend on the same use case layer, not on each
// other.
type Dispatcher interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}
