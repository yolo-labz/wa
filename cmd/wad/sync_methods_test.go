package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// The sync.force/sync.status handlers wrap *whatsmeow.Adapter. The
// adapter's own send/block/timeout behaviour is covered white-box in
// internal/adapters/secondary/whatsmeow/sync_test.go; here we pin the
// thin RPC layer the daemon exposes: param validation happens BEFORE the
// adapter is touched, adapter errors propagate (not swallowed), and the
// success/status payloads carry the locked wire schema.
//
// A zero-value adapter is disconnected (client == nil), so ForceHistorySync
// short-circuits to domain.ErrDisconnected without a real round-trip and
// SyncStatus reports an all-zero snapshot — exactly the deterministic
// surface these handler tests need.

func TestSyncForceHandler_InvalidParams(t *testing.T) {
	t.Parallel()
	h := makeSyncForceHandler(&wmAdapter.Adapter{})
	if _, err := h(context.Background(), json.RawMessage(`{"count":`)); err == nil {
		t.Fatal("expected error on malformed JSON params, got nil")
	}
}

func TestSyncForceHandler_InvalidChat(t *testing.T) {
	t.Parallel()
	h := makeSyncForceHandler(&wmAdapter.Adapter{})
	// "abc@s.whatsapp.net" is rejected by domain.Parse (non-digit user)
	// BEFORE the adapter is consulted, so the handler must surface the
	// parse error rather than a connection error.
	_, err := h(context.Background(), json.RawMessage(`{"chat":"abc@s.whatsapp.net"}`))
	if err == nil {
		t.Fatal("expected error on invalid chat JID, got nil")
	}
	if errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("expected a JID parse error, got adapter-disconnected: %v", err)
	}
}

func TestSyncForceHandler_PropagatesAdapterError(t *testing.T) {
	t.Parallel()
	h := makeSyncForceHandler(&wmAdapter.Adapter{}) // disconnected
	// Valid global pull (no chat) on a disconnected adapter: the handler
	// must propagate ErrDisconnected, not marshal a misleading success.
	out, err := h(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected ErrDisconnected from disconnected adapter, got result: %s", out)
	}
	if !errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("expected errors.Is ErrDisconnected, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil result alongside error, got %s", out)
	}
}

func TestSyncStatusHandler_Shape(t *testing.T) {
	t.Parallel()
	h := makeSyncStatusHandler(&wmAdapter.Adapter{})
	out, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("sync.status handler errored: %v", err)
	}
	var got syncStatusResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal sync.status result: %v", err)
	}
	if got.Schema != "wa.sync.status/v1" {
		t.Errorf("schema = %q, want wa.sync.status/v1", got.Schema)
	}
	if got.Syncing {
		t.Error("idle zero-value adapter reported syncing=true")
	}
	if got.InFlightForceReqs != 0 || got.QueueDepth != 0 {
		t.Errorf("idle adapter reported inFlight=%d queueDepth=%d, want 0/0", got.InFlightForceReqs, got.QueueDepth)
	}
}
