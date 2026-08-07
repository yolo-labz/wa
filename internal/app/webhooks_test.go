package app

// Huly WA-3 done-criterion: a webhook delivery must carry the Standard
// Webhooks 1.0 headers (webhook-id / webhook-timestamp /
// webhook-signature) and the signature must verify with the spec's
// receiver API against the whsec_ secret minted at add time. The
// signing code used the official library since feature 112; no test
// pinned the wire contract — this one does.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// fakeWebhookStore is a minimal WebhookStore for worker delivery tests.
type fakeWebhookStore struct {
	due          []WebhookDue
	markedDeliv  []string
	markedFailed []string
}

func (f *fakeWebhookStore) AddEndpoint(context.Context, WebhookEndpoint) error { return nil }
func (f *fakeWebhookStore) ListEndpoints(context.Context, string) ([]WebhookEndpoint, error) {
	return nil, nil
}

func (f *fakeWebhookStore) EnabledEndpoints(context.Context, string) ([]WebhookEndpoint, error) {
	return nil, nil
}
func (f *fakeWebhookStore) RemoveEndpoint(context.Context, string, string) error { return nil }
func (f *fakeWebhookStore) Enqueue(context.Context, WebhookDelivery) error       { return nil }
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
