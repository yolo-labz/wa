package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// Feature 112 — standard-webhooks outbound delivery.
//
// Push beats SSE-polling for automation platforms (n8n, Home
// Assistant, serverless): the daemon signs every delivery per the
// Standard Webhooks spec (HMAC-SHA256 over `id.timestamp.payload`,
// webhook-id/-timestamp/-signature headers) so any compliant receiver
// verifies with one library call. Delivery state is DB-backed
// (webhooks.db): restarts never lose queued deliveries, failures back
// off exponentially, exhausted deliveries land in a dead-letter state
// replayable via `wa webhook replay`.

// WebhookEndpoint is one registered receiver.
type WebhookEndpoint struct {
	ID            string
	Profile       string
	URL           string
	Secret        string // whsec_… — returned once at add, stored for signing
	Topics        string // comma-separated event types, or "*"
	Disabled      bool
	DisableReason string
	FailureStreak int
	CreatedAt     int64
}

// WebhookDelivery is one queued/settled delivery row.
type WebhookDelivery struct {
	ID            string
	Profile       string
	EndpointID    string
	Topic         string
	Payload       string
	State         string // pending|delivered|dead
	Attempts      int
	NextAttemptAt int64
	LastError     string
	CreatedAt     int64
	UpdatedAt     int64
}

// WebhookDue is the join row the worker consumes.
type WebhookDue struct {
	ID         string
	Profile    string
	EndpointID string
	Topic      string
	Payload    string
	Attempts   int
	URL        string
	Secret     string
}

// WebhookStore is the persistence port backing feature 112.
type WebhookStore interface {
	AddEndpoint(ctx context.Context, ep WebhookEndpoint) error
	ListEndpoints(ctx context.Context, profile string) ([]WebhookEndpoint, error)
	EnabledEndpoints(ctx context.Context, profile string) ([]WebhookEndpoint, error)
	RemoveEndpoint(ctx context.Context, profile, id string) error
	Enqueue(ctx context.Context, d WebhookDelivery) error
	Due(ctx context.Context, profile string, now int64, limit int) ([]WebhookDue, error)
	MarkDelivered(ctx context.Context, profile, id, endpointID string, at int64) error
	MarkFailed(ctx context.Context, profile, id, endpointID string, attempts int, nextAt int64, lastErr string, dead bool, at int64) error
	Deliveries(ctx context.Context, profile, state string, limit int) ([]WebhookDelivery, error)
	Replay(ctx context.Context, profile, id string, now int64) error
}

// ErrWebhookNotFound maps to -32118 webhook_not_found.
var ErrWebhookNotFound = newRPCErr(-32118, "webhook not found")

// webhookBackoffSec is the retry schedule after attempt N (1-based
// index into this slice). 8 attempts spanning ~23h, then dead-letter.
var webhookBackoffSec = []int64{30, 120, 600, 1800, 3600, 10800, 21600, 43200}

// webhookEnvelope is the wire payload (wa.webhook/v1). Data carries
// the subscriber projection — attacker text stays inside its FR-005a
// channel envelope.
type webhookEnvelope struct {
	Schema     string `json:"schema"`
	DeliveryID string `json:"deliveryId"`
	Topic      string `json:"topic"`
	TS         int64  `json:"ts"`
	Data       any    `json:"data"`
}

// NewWebhookSecret mints a standard-webhooks signing secret.
func NewWebhookSecret() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webhook secret: %w", err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(b[:]), nil
}

func newWebhookID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// topicMatch reports whether an endpoint's topic list covers the event
// type. "*" matches everything; otherwise exact, comma-separated.
func topicMatch(topics, eventType string) bool {
	for t := range strings.SplitSeq(topics, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || t == eventType {
			return true
		}
	}
	return false
}

// EventSubscriber is the slice of Dispatcher the worker consumes —
// interface-typed so tests can inject a fake stream.
type EventSubscriber interface {
	SubscribeStream(filter []string, bufSize int) (<-chan Event, func())
}

// WebhookWorker fans bridge events out to registered endpoints and
// drives the delivery queue.
type WebhookWorker struct {
	store   WebhookStore
	events  EventSubscriber
	profile string
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time

	// TickInterval is the due-scan cadence. Default 5s; tests shorten
	// it. Set before Start.
	TickInterval time.Duration

	cancelSub func()
	done      chan struct{}
}

// NewWebhookWorker wires a worker; Start launches its goroutines.
func NewWebhookWorker(store WebhookStore, events EventSubscriber, profile string, log *slog.Logger) *WebhookWorker {
	if log == nil {
		log = slog.Default()
	}
	return &WebhookWorker{
		store:        store,
		events:       events,
		profile:      profile,
		client:       &http.Client{Timeout: 15 * time.Second},
		log:          log,
		now:          time.Now,
		TickInterval: 5 * time.Second,
		done:         make(chan struct{}),
	}
}

// Start launches the fan-out consumer and the delivery loop. Returns
// immediately; Stop unblocks both.
func (w *WebhookWorker) Start(ctx context.Context) {
	ch, cancel := w.events.SubscribeStream(nil, 256)
	w.cancelSub = cancel
	go w.consume(ctx, ch)
	go w.deliverLoop(ctx)
}

// Stop deregisters the event subscriber. In-flight HTTP attempts are
// bounded by the client timeout.
func (w *WebhookWorker) Stop() {
	if w.cancelSub != nil {
		w.cancelSub()
	}
	close(w.done)
}

// consume enqueues one delivery per (event, matching endpoint).
func (w *WebhookWorker) consume(ctx context.Context, ch <-chan Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			w.fanOut(ctx, evt)
		}
	}
}

func (w *WebhookWorker) fanOut(ctx context.Context, evt Event) {
	eps, err := w.store.EnabledEndpoints(ctx, w.profile)
	if err != nil {
		w.log.Warn("webhook fan-out: list endpoints", "error", err)
		return
	}
	if len(eps) == 0 {
		return
	}
	now := w.now().Unix()
	for _, ep := range eps {
		if !topicMatch(ep.Topics, evt.Type) {
			continue
		}
		deliveryID := newWebhookID("whd")
		payload, err := json.Marshal(webhookEnvelope{
			Schema:     "wa.webhook/v1",
			DeliveryID: deliveryID,
			Topic:      evt.Type,
			TS:         now,
			Data:       evt.Payload,
		})
		if err != nil {
			w.log.Warn("webhook fan-out: marshal", "error", err, "type", evt.Type)
			continue
		}
		d := WebhookDelivery{
			ID:            deliveryID,
			Profile:       w.profile,
			EndpointID:    ep.ID,
			Topic:         evt.Type,
			Payload:       string(payload),
			NextAttemptAt: now,
			CreatedAt:     now,
		}
		if err := w.store.Enqueue(ctx, d); err != nil {
			w.log.Warn("webhook fan-out: enqueue", "error", err, "endpoint", ep.ID)
		}
	}
}

// deliverLoop scans for due deliveries every few seconds. DB-backed
// scheduling keeps the queue restart-safe — no in-memory timers.
func (w *WebhookWorker) deliverLoop(ctx context.Context) {
	tick := time.NewTicker(w.TickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-tick.C:
			w.deliverDue(ctx)
		}
	}
}

func (w *WebhookWorker) deliverDue(ctx context.Context) {
	due, err := w.store.Due(ctx, w.profile, w.now().Unix(), 32)
	if err != nil {
		w.log.Warn("webhook deliver: due query", "error", err)
		return
	}
	for _, d := range due {
		w.attempt(ctx, d)
	}
}

// attempt POSTs one delivery with Standard Webhooks signing headers
// and settles the row.
func (w *WebhookWorker) attempt(ctx context.Context, d WebhookDue) {
	now := w.now()
	err := w.post(ctx, d, now)
	if err == nil {
		if mErr := w.store.MarkDelivered(ctx, d.Profile, d.ID, d.EndpointID, now.Unix()); mErr != nil {
			w.log.Warn("webhook deliver: mark delivered", "error", mErr, "delivery", d.ID)
		}
		w.log.Info("webhook delivered", "delivery", d.ID, "endpoint", d.EndpointID, "topic", d.Topic, "attempt", d.Attempts+1)
		return
	}
	attempts := d.Attempts + 1
	dead := attempts >= len(webhookBackoffSec)
	var nextAt int64
	if !dead {
		nextAt = now.Unix() + webhookBackoffSec[attempts-1]
	}
	if mErr := w.store.MarkFailed(ctx, d.Profile, d.ID, d.EndpointID, attempts, nextAt, err.Error(), dead, now.Unix()); mErr != nil {
		w.log.Warn("webhook deliver: mark failed", "error", mErr, "delivery", d.ID)
	}
	w.log.Warn("webhook delivery failed", "delivery", d.ID, "endpoint", d.EndpointID,
		"attempt", attempts, "dead", dead, "error", err)
}

func (w *WebhookWorker) post(ctx context.Context, d WebhookDue, now time.Time) error {
	signer, err := standardwebhooks.NewWebhook(strings.TrimPrefix(d.Secret, "whsec_"))
	if err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	sig, err := signer.Sign(d.ID, now, []byte(d.Payload))
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, d.URL, bytes.NewReader([]byte(d.Payload)))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", d.ID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("webhook-signature", sig)
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("receiver returned %d", resp.StatusCode)
	}
	return nil
}
