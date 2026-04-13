package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/yolo-labz/wa/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/internal/app"
)

// registerHistoryMethods registers the history, messages, search, purge,
// and export JSON-RPC methods on the dispatcher. These methods query
// sqlitehistory.Store directly (not through the HistoryStore port) for
// rich StoredMessage metadata. Feature 009 — FR-014 through FR-016,
// FR-033, FR-036.
func registerHistoryMethods(d *app.Dispatcher, store *sqlitehistory.Store, audit app.AuditLog, log *slog.Logger) {
	d.RegisterMethod("history", makeHistoryHandler(store))
	d.RegisterMethod("messages", makeMessagesHandler(store))
	d.RegisterMethod("search", makeSearchHandler(store))
	d.RegisterMethod("purge", makePurgeHandler(store, log))
	d.RegisterMethod("export", makeExportHandler(store))
}

type historyParams struct {
	Chat   string `json:"chat"`
	Before string `json:"before"`
	Limit  int    `json:"limit"`
}

func makeHistoryHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p historyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.Chat == "" {
			return nil, fmt.Errorf("chat is required")
		}
		if p.Limit <= 0 {
			p.Limit = 50
		}
		msgs, err := store.QueryHistory(ctx, p.Chat, p.Before, p.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

type messagesParams struct {
	Limit int `json:"limit"`
}

func makeMessagesHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p messagesParams
		if raw != nil {
			_ = json.Unmarshal(raw, &p)
		}
		if p.Limit <= 0 {
			p.Limit = 50
		}
		msgs, err := store.QueryMessages(ctx, p.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

type searchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func makeSearchHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p searchParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.Query == "" {
			return nil, fmt.Errorf("query is required")
		}
		if p.Limit <= 0 {
			p.Limit = 20
		}
		msgs, err := store.QuerySearch(ctx, p.Query, p.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

type purgeParams struct {
	Chat string `json:"chat"`
}

func makePurgeHandler(store *sqlitehistory.Store, log *slog.Logger) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p purgeParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.Chat == "" {
			return nil, fmt.Errorf("chat is required")
		}
		deleted, err := store.PurgeChat(ctx, p.Chat)
		if err != nil {
			return nil, err
		}
		log.Info("purge", slog.String("chat", p.Chat), slog.Int64("deleted", deleted))
		return json.Marshal(map[string]any{"deleted": deleted})
	}
}

func makeExportHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p historyParams // reuse — just needs chat
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.Chat == "" {
			return nil, fmt.Errorf("chat is required")
		}
		msgs, err := store.ExportChat(ctx, p.Chat)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

// wireMessage is the JSON shape for the history/messages/search responses.
type wireMessage struct {
	MessageID string `json:"messageId"`
	ChatJID   string `json:"chatJid"`
	SenderJID string `json:"senderJid"`
	Timestamp int64  `json:"timestamp"`
	Body      string `json:"body"`
	MediaType string `json:"mediaType,omitempty"`
	Caption   string `json:"caption,omitempty"`
	IsFromMe  bool   `json:"isFromMe"`
	PushName  string `json:"pushName,omitempty"`
}

func storedToWire(msgs []sqlitehistory.StoredMessage) []wireMessage {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		out[i] = wireMessage{
			MessageID: m.MessageID,
			ChatJID:   m.ChatJID,
			SenderJID: m.SenderJID,
			Timestamp: m.Timestamp,
			Body:      m.Body,
			MediaType: m.MediaType,
			Caption:   m.Caption,
			IsFromMe:  m.IsFromMe,
			PushName:  m.PushName,
		}
	}
	return out
}
