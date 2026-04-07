package socket

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/creachadair/jrpc2"
)

// subscribeParams is the shape of the params object for the "subscribe" method.
type subscribeParams struct {
	Events []string `json:"events"`
}

// subscribeResult is the shape of the result object for the "subscribe" method.
type subscribeResult struct {
	SubscriptionID string `json:"subscriptionId"`
	Schema         string `json:"schema"`
}

// unsubscribeParams is the shape of the params object for the "unsubscribe" method.
type unsubscribeParams struct {
	SubscriptionID string `json:"subscriptionId"`
}

// handleSubscribe implements the "subscribe" JSON-RPC method. It creates a new
// subscription on the connection with a random UUID and registers it in the
// connection's subscription table.
func (s *Server) handleSubscribe(_ context.Context, conn *Connection, params json.RawMessage) (json.RawMessage, error) {
	var p subscribeParams
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: %v", err)
		}
	}
	if p.Events == nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: events is required")
	}

	// Validate that all entries in events are strings (the JSON unmarshal
	// already guarantees this for []string, but we check for empty strings).
	eventSet := make(map[string]struct{}, len(p.Events))
	for _, e := range p.Events {
		eventSet[e] = struct{}{}
	}

	id, err := newUUID()
	if err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
	}

	sub := &Subscription{
		id:        id,
		events:    eventSet,
		createdAt: time.Now(),
	}

	conn.mu.Lock()
	conn.subscriptions[id] = sub
	conn.mu.Unlock()

	conn.log.Info("subscription created", "subscription_id", id, "events", p.Events)

	result := subscribeResult{
		SubscriptionID: id,
		Schema:         "wa.event/v1",
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
	}
	return raw, nil
}

// handleUnsubscribe implements the "unsubscribe" JSON-RPC method. It removes
// the subscription identified by subscriptionId from the connection's table.
func (s *Server) handleUnsubscribe(_ context.Context, conn *Connection, params json.RawMessage) (json.RawMessage, error) {
	var p unsubscribeParams
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: %v", err)
		}
	}
	if p.SubscriptionID == "" {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: subscriptionId is required")
	}

	conn.mu.Lock()
	_, ok := conn.subscriptions[p.SubscriptionID]
	if !ok {
		conn.mu.Unlock()
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: subscription not found")
	}
	delete(conn.subscriptions, p.SubscriptionID)
	conn.mu.Unlock()

	conn.log.Info("subscription removed", "subscription_id", p.SubscriptionID)

	// Return null per the wire protocol contract.
	return nil, nil
}

// newUUID generates a random 16-byte hex-encoded UUID using crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
