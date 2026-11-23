package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// methodWebhookStore is a WebhookStore for the RPC method tests: endpoints
// and deliveries are fixed lists, every call records its arguments, and err
// makes each store call fail.
type methodWebhookStore struct {
	fakeWebhookStore
	added     []WebhookEndpoint
	endpoints []WebhookEndpoint
	rows      []WebhookDelivery
	removed   string
	replayed  string
	state     string
	limit     int
	err       error
}

func (s *methodWebhookStore) AddEndpoint(_ context.Context, ep WebhookEndpoint) error {
	s.added = append(s.added, ep)
	return s.err
}

func (s *methodWebhookStore) ListEndpoints(context.Context, string) ([]WebhookEndpoint, error) {
	return s.endpoints, s.err
}

func (s *methodWebhookStore) RemoveEndpoint(_ context.Context, _, id string) error {
	s.removed = id
	return s.err
}

func (s *methodWebhookStore) Deliveries(_ context.Context, _, state string, limit int) ([]WebhookDelivery, error) {
	s.state, s.limit = state, limit
	return s.rows, s.err
}

func (s *methodWebhookStore) Replay(_ context.Context, _, id string, _ int64) error {
	s.replayed = id
	return s.err
}

func webhookDispatcher(s WebhookStore) *Dispatcher { return &Dispatcher{webhooks: s, profile: "p"} }

func TestWebhookAdd(t *testing.T) {
	t.Parallel()
	store := &methodWebhookStore{}
	d := webhookDispatcher(store)
	raw, err := d.handleWebhookAdd(context.Background(), json.RawMessage(`{"url":"https://example.org/hook"}`))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var out struct{ ID, URL, Topics, Secret string }
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(store.added) != 1 || store.added[0].Topics != "message" || store.added[0].Profile != "p" {
		t.Fatalf("stored %+v, want one endpoint for profile p with the default topic", store.added)
	}
	if !strings.HasPrefix(out.Secret, "whsec_") || out.Secret != store.added[0].Secret || out.ID != store.added[0].ID {
		t.Errorf("result %+v does not match the stored endpoint", out)
	}
	if strings.Contains(string(raw), "\"Secret\"") {
		t.Errorf("unexpected raw field in %s", raw)
	}
}

func TestWebhookAdd_Refusals(t *testing.T) {
	t.Parallel()
	d := webhookDispatcher(&methodWebhookStore{})
	for _, p := range []string{`{"url":"ftp://example.org"}`, `{"url":"https://"}`, `{"url":"::"}`, `{}`} {
		if _, err := d.handleWebhookAdd(context.Background(), json.RawMessage(p)); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("add %s: err = %v, want invalid params", p, err)
		}
	}
	if _, err := d.handleWebhookAdd(context.Background(), json.RawMessage(`{"url":`)); err == nil {
		t.Error("add with broken JSON: no error")
	}
	failing := webhookDispatcher(&methodWebhookStore{err: errors.New("disk")})
	if _, err := failing.handleWebhookAdd(context.Background(), json.RawMessage(`{"url":"https://example.org","topics":" a,b "}`)); err == nil || !strings.Contains(err.Error(), "webhook.add") {
		t.Errorf("store failure: err = %v, want a webhook.add error", err)
	}
}

func TestWebhookList_HidesSecrets(t *testing.T) {
	t.Parallel()
	store := &methodWebhookStore{endpoints: []WebhookEndpoint{{ID: "whe_1", URL: "https://example.org", Topics: "message", Secret: "whsec_x", Disabled: true, DisableReason: "dead", FailureStreak: 3}}}
	raw, err := webhookDispatcher(store).handleWebhookList(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "whsec_x") || !strings.Contains(string(raw), "whe_1") || !strings.Contains(string(raw), "dead") {
		t.Errorf("list = %s", raw)
	}
	store.err = errors.New("disk")
	if _, err := webhookDispatcher(store).handleWebhookList(context.Background(), nil); err == nil {
		t.Error("list with a failing store: no error")
	}
}

func TestWebhookRemoveAndReplay(t *testing.T) {
	t.Parallel()
	store := &methodWebhookStore{}
	d := webhookDispatcher(store)
	for name, call := range map[string]func(context.Context, json.RawMessage) (json.RawMessage, error){
		"remove": d.handleWebhookRemove, "replay": d.handleWebhookReplay,
	} {
		if raw, err := call(context.Background(), json.RawMessage(`{"id":"x1"}`)); err != nil || !strings.Contains(string(raw), "x1") {
			t.Errorf("%s: %s %v", name, raw, err)
		}
		if _, err := call(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("%s with no id: err = %v", name, err)
		}
		if _, err := call(context.Background(), json.RawMessage(`{"id":`)); err == nil {
			t.Errorf("%s with broken JSON: no error", name)
		}
		store.err = ErrWebhookNotFound
		if _, err := call(context.Background(), json.RawMessage(`{"id":"x1"}`)); !errors.Is(err, ErrWebhookNotFound) {
			t.Errorf("%s: err = %v, want webhook not found", name, err)
		}
		store.err = nil
	}
	if store.removed != "x1" || store.replayed != "x1" {
		t.Errorf("store saw remove %q replay %q", store.removed, store.replayed)
	}
}

func TestWebhookDeliveries(t *testing.T) {
	t.Parallel()
	store := &methodWebhookStore{rows: []WebhookDelivery{{ID: "d1", EndpointID: "whe_1", Topic: "message", State: "dead", Attempts: 8, LastError: "500"}}}
	d := webhookDispatcher(store)
	raw, err := d.handleWebhookDeliveries(context.Background(), json.RawMessage(`{"state":"dead","limit":500}`))
	if err != nil || !strings.Contains(string(raw), "d1") {
		t.Fatalf("deliveries: %s %v", raw, err)
	}
	if store.state != "dead" || store.limit != 50 {
		t.Errorf("store saw state %q limit %d, want dead and the default 50 for an out-of-range limit", store.state, store.limit)
	}
	if _, err := d.handleWebhookDeliveries(context.Background(), nil); err != nil || store.limit != 50 {
		t.Errorf("no params: %v limit %d", err, store.limit)
	}
	for _, p := range []string{`{"state":"bogus"}`, `{"state":`} {
		if _, err := d.handleWebhookDeliveries(context.Background(), json.RawMessage(p)); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("deliveries %s: err = %v", p, err)
		}
	}
	store.err = errors.New("disk")
	if _, err := d.handleWebhookDeliveries(context.Background(), nil); err == nil {
		t.Error("deliveries with a failing store: no error")
	}
}

func TestWebhookMethods_NoStoreIsMethodNotFound(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{}
	ctx := context.Background()
	for name, call := range map[string]func(context.Context, json.RawMessage) (json.RawMessage, error){
		"add": d.handleWebhookAdd, "list": d.handleWebhookList, "remove": d.handleWebhookRemove,
		"deliveries": d.handleWebhookDeliveries, "replay": d.handleWebhookReplay,
	} {
		if _, err := call(ctx, json.RawMessage(`{}`)); !errors.Is(err, ErrMethodNotFound) {
			t.Errorf("%s: err = %v, want method not found", name, err)
		}
	}
}
