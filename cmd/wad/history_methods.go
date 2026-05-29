package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/app"
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
	d.RegisterMethod("chat.list", makeChatListHandler(store))
	d.RegisterMethod("messages.list", makeMessagesListHandler(store))
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
			return nil, errors.New("chat is required")
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
			return nil, errors.New("query is required")
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
			return nil, errors.New("chat is required")
		}
		deleted, err := store.PurgeChat(ctx, p.Chat)
		if err != nil {
			return nil, err
		}
		log.Info("purge", slog.String("chat", p.Chat), slog.Int64("deleted", deleted))
		return json.Marshal(map[string]any{"deleted": deleted})
	}
}

type exportParams struct {
	Chat  string `json:"chat"`
	Since int64  `json:"since"`
	Until int64  `json:"until"`
	Limit int    `json:"limit"`
}

func makeExportHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p exportParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.Chat == "" {
			return nil, errors.New("chat is required")
		}
		msgs, err := store.ExportChatFiltered(ctx, p.Chat, p.Since, p.Until, p.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

type chatListParams struct {
	Limit int `json:"limit"`
}

func makeChatListHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p chatListParams
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &p)
		}
		chats, err := store.QueryChats(ctx, p.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"chats": chatsToWire(chats)})
	}
}

type messagesListParams struct {
	Chat      string `json:"chat"`
	MediaType string `json:"mediaType"`
	FromMe    *bool  `json:"fromMe"`
	Since     int64  `json:"since"`
	Until     int64  `json:"until"`
	Limit     int    `json:"limit"`
}

func makeMessagesListHandler(store *sqlitehistory.Store) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p messagesListParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
		}
		msgs, err := store.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{
			ChatJID:       p.Chat,
			MediaTypeLike: mediaTypeLikePattern(p.MediaType),
			FromMe:        p.FromMe,
			Since:         p.Since,
			Until:         p.Until,
			Limit:         p.Limit,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"messages": storedToWire(msgs)})
	}
}

// mediaTypeLikePattern maps a friendly --media-type token to a SQL LIKE
// pattern over the stored MIME. "audio"/"video"/"image" become "<x>/%";
// "pdf" maps to application/pdf; a value already containing "/" or "%" is
// used verbatim; "" stays "" (no filter). Issue #173.
func mediaTypeLikePattern(s string) string {
	switch s {
	case "":
		return ""
	case "audio", "video", "image":
		return s + "/%"
	case "pdf":
		return "application/pdf"
	default:
		if strings.ContainsAny(s, "/%") {
			return s
		}
		return s + "/%"
	}
}

// chatWire is the JSON shape for chat.list / chat.last-active rows.
type chatWire struct {
	JID           string `json:"jid"`
	PushName      string `json:"pushName,omitempty"`
	LastMessageTS int64  `json:"lastMessageTs"`
	MessageCount  int64  `json:"messageCount"`
	IsGroup       bool   `json:"isGroup"`
}

func chatsToWire(cs []sqlitehistory.ChatSummary) []chatWire {
	out := make([]chatWire, len(cs))
	for i, c := range cs {
		out[i] = chatWire{
			JID:           c.ChatJID,
			PushName:      c.PushName,
			LastMessageTS: c.LastMessageTS,
			MessageCount:  c.MessageCount,
			IsGroup:       c.IsGroup,
		}
	}
	return out
}

// wireMessage is the JSON shape for the history/messages/search responses.
//
// Spec 107 added two optional fields surfacing the PN/LID duality
// whatsmeow tracks per-message:
//   - SenderAltJID: the alternate-namespace JID for SenderJID. PN if
//     SenderJID is a LID, LID if SenderJID is a PN. Empty when whatsmeow
//     has not yet learned the mapping (most legacy and history-sync
//     rows). Callers MUST treat empty as "unknown" without erroring.
//   - AddressingMode: "pn" or "lid", indicating which namespace the
//     sender was addressed by on the wire. Empty on legacy rows.
type wireMessage struct {
	MessageID      string `json:"messageId"`
	ChatJID        string `json:"chatJid"`
	SenderJID      string `json:"senderJid"`
	Timestamp      int64  `json:"timestamp"`
	Body           string `json:"body"`
	MediaType      string `json:"mediaType,omitempty"`
	Caption        string `json:"caption,omitempty"`
	IsFromMe       bool   `json:"isFromMe"`
	PushName       string `json:"pushName,omitempty"`
	SenderAltJID   string `json:"senderAltJid,omitempty"`
	AddressingMode string `json:"addressingMode,omitempty"`
}

func storedToWire(msgs []sqlitehistory.StoredMessage) []wireMessage {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		out[i] = wireMessage{
			MessageID:      m.MessageID,
			ChatJID:        m.ChatJID,
			SenderJID:      m.SenderJID,
			Timestamp:      m.Timestamp,
			Body:           m.Body,
			MediaType:      m.MediaType,
			Caption:        m.Caption,
			IsFromMe:       m.IsFromMe,
			PushName:       m.PushName,
			SenderAltJID:   m.SenderAltJID,
			AddressingMode: m.AddressingMode,
		}
	}
	return out
}
