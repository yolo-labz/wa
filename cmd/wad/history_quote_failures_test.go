package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

func TestPlainProjectionNeverExposesUnvalidatedQuote(t *testing.T) {
	msgs := []sqlitehistory.StoredMessage{{ChatJID: "11111111@s.whatsapp.net", MessageID: "REPLY", RawProto: quotedTextProto(t, "reply", "OTHER")}}
	got := storedToWire(msgs)
	if got[0].QuotedMessageID != "" {
		t.Fatal("plain projection exposed an unvalidated quote")
	}
}

func TestQuoteLookupFailureDoesNotPublishPartialProjection(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	msgs := []sqlitehistory.StoredMessage{{ChatJID: "11111111@s.whatsapp.net", MessageID: "REPLY", RawProto: quotedTextProto(t, "reply", "OTHER")}}
	got, err := storedToWireValidated(ctx, store, msgs)
	if got != nil || err == nil {
		t.Fatal("failed DB lookup published success-shaped projection")
	}
}

func TestQuoteProjectionCancellationAndPageReuse(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Already-materialized same-chat targets do not need another DB query.
	msgs := []sqlitehistory.StoredMessage{
		{ChatJID: "11111111@s.whatsapp.net", MessageID: "PHOTO"},
		{ChatJID: "11111111@s.whatsapp.net", MessageID: "REPLY", RawProto: quotedTextProto(t, "reply", "PHOTO")},
	}
	got, err := storedToWireValidated(ctx, store, msgs)
	if err != nil || got[1].QuotedMessageID != "PHOTO" {
		t.Fatalf("page reuse: %v, %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := storedToWireValidated(canceled, store, msgs); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled projection: %v, %v", got, err)
	}
	if got, err := storedToWireValidated(ctx, nil, msgs); got != nil || err == nil {
		t.Fatalf("nil store accepted: %v, %v", got, err)
	}
}
