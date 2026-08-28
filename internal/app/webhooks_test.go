package app

// Huly WA-3 done-criterion: a webhook delivery must carry the Standard
// Webhooks 1.0 headers (webhook-id / webhook-timestamp /
// webhook-signature) and the signature must verify with the spec's
// receiver API against the whsec_ secret minted at add time. The
// signing code used the official library since feature 112; no test
// pinned the wire contract — this one does.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeWebhookStore is a minimal WebhookStore for worker delivery tests.
type fakeWebhookStore struct {
	due          []WebhookDue
	enabled      []WebhookEndpoint
	enqueued     []WebhookDelivery
	markedDeliv  []string
	markedFailed []string
}

func (f *fakeWebhookStore) AddEndpoint(context.Context, WebhookEndpoint) error { return nil }
func (f *fakeWebhookStore) ListEndpoints(context.Context, string) ([]WebhookEndpoint, error) {
	return nil, nil
}

func (f *fakeWebhookStore) EnabledEndpoints(context.Context, string) ([]WebhookEndpoint, error) {
	return f.enabled, nil
}
func (f *fakeWebhookStore) RemoveEndpoint(context.Context, string, string) error { return nil }

func (f *fakeWebhookStore) Enqueue(_ context.Context, d WebhookDelivery) error {
	f.enqueued = append(f.enqueued, d)
	return nil
}

func (f *fakeWebhookStore) Due(context.Context, string, int64, int) ([]WebhookDue, error) {
	return f.due, nil
}

func (f *fakeWebhookStore) MarkDelivered(_ context.Context, _ string, id, _ string, _ int64) error {
	f.markedDeliv = append(f.markedDeliv, id)
	return nil
}

func (f *fakeWebhookStore) MarkFailed(_ context.Context, _ string, id, _ string, attempts int,
	nextAt int64, _ string, dead bool, _ int64,
) error {
	f.markedFailed = append(f.markedFailed, id)
	return nil
}

func (f *fakeWebhookStore) Deliveries(context.Context, string, string, int) ([]WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookStore) Replay(context.Context, string, string, int64) error { return nil }

func newWebhookTestWorker(t *testing.T, secret string, url string) (*WebhookWorker, *fakeWebhookStore) {
	t.Helper()
	store := &fakeWebhookStore{due: []WebhookDue{{
		ID: "whd_1", Profile: "p", EndpointID: "ep-1", Topic: "message",
		Payload:  `{"schema":"wa.webhook/v1","deliveryId":"whd_1","topic":"message","ts":1752000000,"data":{"channel":"FR-005a","text":"hi"}}`,
		Attempts: 0, URL: url, Secret: secret,
	}}}
	w := NewWebhookWorker(store, nil, "p", slog.New(slog.DiscardHandler))
	return w, store
}

// TestWebhookDeliveryCarriesStandardHeaders: the falsifier for huly WA-3.
// A compliant consumer must be able to verify the delivery; the headers
// must be exactly the spec's three.
func TestWebhookDeliveryCarriesStandardHeaders(t *testing.T) {
	secret, err := NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}

	var gotID, gotTS, gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("webhook-id")
		gotTS = r.Header.Get("webhook-timestamp")
		gotSig = r.Header.Get("webhook-signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w, store := newWebhookTestWorker(t, secret, srv.URL)
	w.deliverDue(context.Background())

	if gotID != "whd_1" {
		t.Errorf("webhook-id = %q, want whd_1", gotID)
	}
	ts, err := strconv.ParseInt(gotTS, 10, 64)
	if err != nil {
		t.Fatalf("webhook-timestamp = %q, not an epoch: %v", gotTS, err)
	}
	if d := time.Since(time.Unix(ts, 0)); d > 30*time.Second || d < -30*time.Second {
		t.Errorf("webhook-timestamp %d outside ±30s of now", ts)
	}
	if gotSig == "" {
		t.Fatal("webhook-signature header empty")
	}
	if !strings.HasPrefix(gotSig, "v1,") {
		t.Errorf("webhook-signature = %q, want v1,... prefix", gotSig)
	}

	// Receiver-side verification — the spec contract: a consumer with
	// the secret must accept this delivery.
	wh, err := standardwebhooks.NewWebhook(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatalf("NewWebhook(receiver): %v", err)
	}
	headers := make(http.Header)
	headers.Set("webhook-id", gotID)
	headers.Set("webhook-timestamp", gotTS)
	headers.Set("webhook-signature", gotSig)
	if err := wh.Verify(gotBody, headers); err != nil {
		t.Errorf("Standard Webhooks receiver rejected the delivery: %v", err)
	}

	if len(store.markedDeliv) != 1 || store.markedDeliv[0] != "whd_1" {
		t.Errorf("expected MarkDelivered(whd_1), got %v", store.markedDeliv)
	}
	if len(store.markedFailed) != 0 {
		t.Errorf("unexpected MarkFailed calls: %v", store.markedFailed)
	}
}

// TestWebhookDeliveryNon2xxFails: a non-2xx response must settle the
// row as failed with a backoff, not as delivered.
func TestWebhookDeliveryNon2xxFails(t *testing.T) {
	secret, err := NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w, store := newWebhookTestWorker(t, secret, srv.URL)
	w.deliverDue(context.Background())

	if len(store.markedFailed) != 1 || store.markedFailed[0] != "whd_1" {
		t.Errorf("expected MarkFailed(whd_1), got %v", store.markedFailed)
	}
	if len(store.markedDeliv) != 0 {
		t.Errorf("unexpected MarkDelivered calls: %v", store.markedDeliv)
	}
}

// TestWebhookFanOutCarriesSourceMessageID walks the whole chain a signed
// audio webhook actually takes — domain event → subscriber projection →
// `wa.webhook/v1` envelope — and asserts the delivery body a receiver
// parses carries `data.messageId`. Without it, an n8n/agent consumer
// reading a voice-note webhook has no argument for `media.download
// {messageId, transcribe:true}`: `data.id` is the EventBridge sequence
// number and that method does not accept it.
//
// The envelope is also asserted to still be body-only-firewalled: the
// new field is a structural id, so nothing sender-authored may appear
// outside `data.channel`.
func TestWebhookFanOutCarriesSourceMessageID(t *testing.T) {
	chat := domain.MustJID("12025550100@s.whatsapp.net")
	store := &fakeWebhookStore{enabled: []WebhookEndpoint{{
		ID: "ep-1", Profile: "p", URL: "https://n8n.example/hook", Topics: "message",
	}}}
	w := NewWebhookWorker(store, nil, "p", slog.New(slog.DiscardHandler))

	w.fanOut(context.Background(), translateDomainEvent(domain.MessageEvent{
		ID:        "6252",
		MessageID: "AC3628D1F0B4A9E75C11",
		TS:        time.Unix(1781000020, 0),
		From:      chat,
		PushName:  "Cafe",
		Message:   domain.AudioMessage{Recipient: chat, Path: "/a.ogg", Mime: "audio/ogg", Seconds: 7, PTT: true},
	}))

	if len(store.enqueued) != 1 {
		t.Fatalf("enqueued %d deliveries, want 1", len(store.enqueued))
	}
	var env struct {
		Schema string `json:"schema"`
		Topic  string `json:"topic"`
		Data   struct {
			ID        string `json:"id"`
			MessageID string `json:"messageId"`
			Kind      string `json:"kind"`
			Channel   string `json:"channel"`
		} `json:"data"`
	}
	body := store.enqueued[0].Payload
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal delivery payload: %v", err)
	}
	if env.Schema != "wa.webhook/v1" || env.Topic != "message" {
		t.Errorf("envelope = schema %q topic %q", env.Schema, env.Topic)
	}
	if env.Data.MessageID != "AC3628D1F0B4A9E75C11" {
		t.Errorf("data.messageId = %q, want the stanza id AC3628D1F0B4A9E75C11", env.Data.MessageID)
	}
	if env.Data.ID != "6252" {
		t.Errorf("data.id = %q, want the event sequence number 6252", env.Data.ID)
	}
	if env.Data.Kind != "audio" {
		t.Errorf("data.kind = %q, want audio", env.Data.Kind)
	}
	if !strings.Contains(env.Data.Channel, `<channel source="wa"`) {
		t.Errorf("data.channel is not the FR-005a envelope: %q", env.Data.Channel)
	}
	for _, forbidden := range []string{`"body":`, `"caption":`, `"pushName"`, `"path":`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("delivery body leaks %s: %s", forbidden, body)
		}
	}
}
