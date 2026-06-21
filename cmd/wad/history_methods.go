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
	"github.com/yolo-labz/wa/v2/internal/domain"
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
// Prompt-injection firewall (SEC, FR-005a): for INBOUND rows (!IsFromMe)
// every attacker-controllable string — body, caption, pushName, and any
// interactive prompt/option labels — is folded into the Channel envelope
// exactly once via app.ChannelWrapFields, and the raw Body/Caption/
// PushName are left empty so no agent/MCP/CLI consumer can read unwrapped
// text by accident. This mirrors the live SubscriberMessageEvent
// projection. OUTBOUND rows are our own text and pass through raw.
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
	// Interactive surfaces the list/button/native-flow reply selection
	// when the message was an interactive reply (issue #201, FR-130), and
	// is omitted for ordinary messages. It lets a client read which menu
	// option the user picked (the selected id + label).
	Interactive *wireInteractive `json:"interactive,omitempty"`
	// Channel is the FR-005a `<channel source="wa">` envelope holding every
	// attacker-controllable string of an INBOUND message. Present only for
	// inbound rows; empty (and omitted) for outbound (our own) messages.
	Channel string `json:"channel,omitempty"`
}

// wireInteractive is the client JSON shape for a persisted interactive
// reply selection (issue #201). subtype is "list" or "buttons"; options
// carries the selected row/button id + its human label.
type wireInteractive struct {
	Subtype string                  `json:"subtype"`
	Prompt  string                  `json:"prompt,omitempty"`
	Options []wireInteractiveOption `json:"options"`
}

type wireInteractiveOption struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

func storedToWire(msgs []sqlitehistory.StoredMessage) []wireMessage {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		w := wireMessage{
			MessageID:      m.MessageID,
			ChatJID:        m.ChatJID,
			SenderJID:      m.SenderJID,
			Timestamp:      m.Timestamp,
			MediaType:      m.MediaType,
			IsFromMe:       m.IsFromMe,
			SenderAltJID:   m.SenderAltJID,
			AddressingMode: m.AddressingMode,
		}
		if m.IsFromMe {
			// Outbound: our own text, trusted — pass through raw.
			w.Body = m.Body
			w.Caption = m.Caption
			w.PushName = m.PushName
			w.Interactive = interactiveToWire(m.InteractiveJSON)
		} else {
			// Inbound: attacker-controllable. Fold every untrusted string
			// into one <channel source="wa"> envelope (FR-005a) and leave
			// the raw fields empty so no consumer reads them unwrapped.
			w.Channel, w.Interactive = wrapStoredInbound(m)
		}
		out[i] = w
	}
	return out
}

// wrapStoredInbound folds the attacker-controllable fields of an inbound
// stored message — body, caption, pushName, and any interactive prompt +
// option labels — into a single `<channel source="wa">` envelope
// (app.ChannelWrapFields, FR-005a). It returns that envelope plus a
// structural-only interactive view: subtype + option IDs are kept (a bot
// needs them to correlate the selection) while the human-readable prompt
// and labels are stripped — they live, escaped, inside the envelope. The
// raw untrusted text never leaves this function unwrapped.
func wrapStoredInbound(m sqlitehistory.StoredMessage) (string, *wireInteractive) {
	fields := app.InboundFields{
		Body:     m.Body,
		Caption:  m.Caption,
		PushName: m.PushName,
	}

	var iv *wireInteractive
	if p, err := sqlitehistory.UnmarshalInteractive(m.InteractiveJSON); err == nil && p != nil {
		fields.PollQuestion = p.Prompt
		opts := make([]wireInteractiveOption, 0, len(p.Options))
		labels := make([]string, 0, len(p.Options))
		for _, o := range p.Options {
			opts = append(opts, wireInteractiveOption{ID: o.ID}) // Label omitted: in Channel.
			labels = append(labels, o.Label)
		}
		fields.PollOptions = labels
		iv = &wireInteractive{Subtype: p.Subtype.String(), Options: opts}
	}

	// JIDs are stored from real messages so Parse normally succeeds; on a
	// malformed legacy row we fall back to the zero JID (empty attribute).
	// Either way the untrusted body is still escaped — the part the
	// firewall actually depends on.
	chat, _ := domain.Parse(m.ChatJID)
	sender, _ := domain.Parse(m.SenderJID)
	return app.ChannelWrapFields(fields, chat, sender, m.Timestamp), iv
}

// interactiveToWire decodes the persisted interactive_json BLOB into the
// client wire shape (issue #201). Returns nil for ordinary messages and —
// defensively — for a malformed/legacy BLOB, so one bad row never fails an
// entire history page (the decode error is intentionally swallowed; the
// raw bytes remain in the database for re-processing).
func interactiveToWire(b []byte) *wireInteractive {
	p, err := sqlitehistory.UnmarshalInteractive(b)
	if err != nil || p == nil {
		return nil
	}
	w := &wireInteractive{
		Subtype: p.Subtype.String(),
		Prompt:  p.Prompt,
		Options: make([]wireInteractiveOption, 0, len(p.Options)),
	}
	for _, o := range p.Options {
		w.Options = append(w.Options, wireInteractiveOption{ID: o.ID, Label: o.Label})
	}
	return w
}
