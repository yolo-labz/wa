package socket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/creachadair/jrpc2"
)

// subscribeParams is the shape of the params object for the "subscribe" method.
// Feature 017 — FR-060: filter DSL with Kafka-style Since resume cursor.
type subscribeParams struct {
	Events     []string `json:"events"`
	Chats      []string `json:"chats,omitempty"`
	Senders    []string `json:"senders,omitempty"`
	NotSenders []string `json:"notSenders,omitempty"`
	BodyRe     string   `json:"bodyRe,omitempty"`
	Since      int64    `json:"since,omitempty"`
}

// subscribeResult is the shape of the result object for the "subscribe" method.
type subscribeResult struct {
	SubscriptionID string `json:"subscriptionId"`
	Schema         string `json:"schema"`
	Since          int64  `json:"since,omitempty"`
}

// subscriptionRef is the shape of the params object for every method that
// names an existing subscription — "unsubscribe" and "subscribe.pong". They
// take byte-identical params, so sharing the type keeps them from drifting
// apart on how a malformed reference is reported.
type subscriptionRef struct {
	SubscriptionID string `json:"subscriptionId"`
}

// withSubscription resolves a subscriptionRef against the connection and runs
// fn on the named subscription while conn.mu is held, returning the id so the
// caller can log outside the lock.
//
// Both methods that name an existing subscription need the same decode →
// lock → look up → not-found prologue, and each one's mutation is only safe
// under that same lock — so the callback runs inside it rather than after,
// which is what keeps the lookup and the mutation from being two separate
// critical sections with a window between them.
func withSubscription(conn *Connection, params json.RawMessage, fn func(id string, sub *Subscription)) (string, error) {
	var p subscriptionRef
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return "", jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: %v", err)
		}
	}
	if p.SubscriptionID == "" {
		return "", jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: subscriptionId is required")
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	sub, ok := conn.subscriptions[p.SubscriptionID]
	if !ok {
		return "", jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: subscription not found")
	}
	fn(p.SubscriptionID, sub)
	return p.SubscriptionID, nil
}

// handleSubscribe implements the "subscribe" JSON-RPC method. It creates a new
// subscription on the connection with a random UUID and registers it in the
// connection's subscription table.
func (s *Server) handleSubscribe(ctx context.Context, conn *Connection, params json.RawMessage) (json.RawMessage, error) {
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

	var bodyReCompiled *regexp.Regexp
	if p.BodyRe != "" {
		re, reErr := regexp.Compile(p.BodyRe)
		if reErr != nil {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "Invalid params: bodyRe: %v", reErr)
		}
		bodyReCompiled = re
	}

	id, err := newUUID()
	if err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInternalError), "Internal error")
	}

	now := time.Now()
	sub := &Subscription{
		id:             id,
		events:         eventSet,
		chats:          append([]string(nil), p.Chats...),
		senders:        append([]string(nil), p.Senders...),
		notSenders:     append([]string(nil), p.NotSenders...),
		bodyRe:         p.BodyRe,
		bodyReCompiled: bodyReCompiled,
		since:          p.Since,
		lastSeq:        p.Since,
		lastPongAt:     now,
		createdAt:      now,
	}

	// FR-061 resume, before registration so the replayed frames are
	// already queued in the connection's FIFO mailbox when the first live
	// frame arrives. See replaySubscription for why the order matters.
	if s.replay != nil && sub.since > 0 {
		s.replaySubscription(ctx, conn, sub)
	}

	conn.mu.Lock()
	conn.subscriptions[id] = sub
	conn.mu.Unlock()

	conn.log.Info("subscription created", "subscription_id", id, "events", p.Events)

	result := subscribeResult{
		SubscriptionID: id,
		Schema:         "wa.event/v1",
		Since:          sub.since,
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
	id, err := withSubscription(conn, params, func(id string, _ *Subscription) {
		delete(conn.subscriptions, id)
	})
	if err != nil {
		return nil, err
	}

	conn.log.Info("subscription removed", "subscription_id", id)

	// Return null per the wire protocol contract.
	return nil, nil
}

// newUUID generates a random 16-byte hex-encoded UUID using crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handlePong implements the "subscribe.pong" JSON-RPC method. It refreshes
// the subscription's lastPongAt timestamp so the reaper does not close it.
// The client sends it in response to a server-emitted subscribe.ping frame.
// Returns null per the wire protocol contract.
func (s *Server) handlePong(_ context.Context, conn *Connection, params json.RawMessage) (json.RawMessage, error) {
	if _, err := withSubscription(conn, params, func(_ string, sub *Subscription) {
		sub.lastPongAt = time.Now()
	}); err != nil {
		return nil, err
	}
	return nil, nil
}
