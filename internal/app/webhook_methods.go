package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Feature 112 — webhook management JSON-RPC methods. Endpoint
// mutation is admin-scoped on the REST surface (an endpoint is a data
// EGRESS destination); see rest/scope.go.

type webhookEndpointView struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Topics        string `json:"topics"`
	Disabled      bool   `json:"disabled,omitempty"`
	DisableReason string `json:"disableReason,omitempty"`
	FailureStreak int    `json:"failureStreak,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

type webhookDeliveryView struct {
	ID            string `json:"id"`
	EndpointID    string `json:"endpointId"`
	Topic         string `json:"topic"`
	State         string `json:"state"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"nextAttemptAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type webhookAddParams struct {
	URL    string `json:"url"`
	Topics string `json:"topics,omitempty"`
}

// handleWebhookAdd implements "webhook.add". The signing secret is
// returned EXACTLY ONCE; the daemon keeps it only for signing.
func (d *Dispatcher) handleWebhookAdd(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.webhooks == nil {
		return nil, ErrMethodNotFound
	}
	var p webhookAddParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, ErrInvalidParams
	}
	topics := strings.TrimSpace(p.Topics)
	if topics == "" {
		topics = "message"
	}
	secret, err := NewWebhookSecret()
	if err != nil {
		return nil, fmt.Errorf("webhook.add: %w", err)
	}
	ep := WebhookEndpoint{
		ID:        newWebhookID("whe"),
		Profile:   d.profile,
		URL:       p.URL,
		Secret:    secret,
		Topics:    topics,
		CreatedAt: time.Now().Unix(),
	}
	if err := d.webhooks.AddEndpoint(ctx, ep); err != nil {
		return nil, fmt.Errorf("webhook.add: %w", err)
	}
	return marshalResult(struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Topics string `json:"topics"`
		Secret string `json:"secret"`
		Note   string `json:"note"`
	}{ep.ID, ep.URL, ep.Topics, secret, "store this secret now — it is not shown again; verify deliveries with any standard-webhooks library"})
}

// handleWebhookList implements "webhook.list" (secrets omitted).
func (d *Dispatcher) handleWebhookList(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if d.webhooks == nil {
		return nil, ErrMethodNotFound
	}
	eps, err := d.webhooks.ListEndpoints(ctx, d.profile)
	if err != nil {
		return nil, fmt.Errorf("webhook.list: %w", err)
	}
	out := make([]webhookEndpointView, 0, len(eps))
	for _, ep := range eps {
		out = append(out, webhookEndpointView{
			ID: ep.ID, URL: ep.URL, Topics: ep.Topics,
			Disabled: ep.Disabled, DisableReason: ep.DisableReason,
			FailureStreak: ep.FailureStreak, CreatedAt: ep.CreatedAt,
		})
	}
	return marshalResult(struct {
		Endpoints []webhookEndpointView `json:"endpoints"`
	}{out})
}

type webhookIDParams struct {
	ID string `json:"id"`
}

// handleWebhookRemove implements "webhook.remove".
func (d *Dispatcher) handleWebhookRemove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.webhooks == nil {
		return nil, ErrMethodNotFound
	}
	var p webhookIDParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	if err := d.webhooks.RemoveEndpoint(ctx, d.profile, p.ID); err != nil {
		return nil, err
	}
	return marshalResult(struct {
		Removed string `json:"removed"`
	}{p.ID})
}

type webhookDeliveriesParams struct {
	State string `json:"state,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// handleWebhookDeliveries implements "webhook.deliveries".
func (d *Dispatcher) handleWebhookDeliveries(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.webhooks == nil {
		return nil, ErrMethodNotFound
	}
	p := webhookDeliveriesParams{Limit: 50}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	switch p.State {
	case "", "pending", "delivered", "dead":
	default:
		return nil, ErrInvalidParams
	}
	rows, err := d.webhooks.Deliveries(ctx, d.profile, p.State, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("webhook.deliveries: %w", err)
	}
	out := make([]webhookDeliveryView, 0, len(rows))
	for _, r := range rows {
		out = append(out, webhookDeliveryView{
			ID: r.ID, EndpointID: r.EndpointID, Topic: r.Topic, State: r.State,
			Attempts: r.Attempts, NextAttemptAt: r.NextAttemptAt,
			LastError: r.LastError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return marshalResult(struct {
		Deliveries []webhookDeliveryView `json:"deliveries"`
	}{out})
}

// handleWebhookReplay implements "webhook.replay" — re-arms one
// delivery (including dead-lettered) for an immediate attempt.
func (d *Dispatcher) handleWebhookReplay(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.webhooks == nil {
		return nil, ErrMethodNotFound
	}
	var p webhookIDParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, ErrInvalidParams
	}
	if err := d.webhooks.Replay(ctx, d.profile, p.ID, time.Now().Unix()); err != nil {
		return nil, err
	}
	return marshalResult(struct {
		Replayed string `json:"replayed"`
	}{p.ID})
}
