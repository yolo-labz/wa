package main

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// registerMediaListMethod wires "media.list" on the dispatcher. The method
// needs direct access to BOTH the history store (to enumerate media-bearing
// rows) and the media adapter (to re-parse each row's proto for
// sha/size/duration and probe the cache), so it lives in the composition
// root rather than on *app.Dispatcher. Issue #173.
func registerMediaListMethod(d *app.Dispatcher, store *sqlitehistory.Store, media *wmAdapter.MediaAdapter) {
	d.RegisterMethod("media.list", makeMediaListHandler(store, media))
}

type mediaListParams struct {
	Chat      string `json:"chat"`
	MediaType string `json:"mediaType"`
	Limit     int    `json:"limit"`
}

// mediaWire is the JSON shape for one media.list row. Size/sha/duration come
// from the proto envelope; Cached/Path/CachedSize reflect the local cache.
type mediaWire struct {
	MessageID string `json:"messageId"`
	ChatJID   string `json:"chatJid"`
	Timestamp int64  `json:"timestamp"`
	MediaType string `json:"mediaType,omitempty"`
	Caption   string `json:"caption,omitempty"`
	// Channel holds the FR-005a `<channel source="wa">` envelope wrapping an
	// INBOUND media caption (attacker-controllable). Present only for inbound
	// rows with a caption; Caption is then empty. Outbound captions pass raw.
	Channel         string `json:"channel,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	Size            int64  `json:"size,omitempty"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	Cached          bool   `json:"cached"`
	Path            string `json:"path,omitempty"`
	CachedSize      int64  `json:"cachedSize,omitempty"`
}

// mediaRowBase builds the history-derived portion of a media.list row. An
// INBOUND caption is attacker-controllable, so it is folded into the
// `<channel source="wa">` firewall envelope (FR-005a) and the raw Caption is
// left empty; our own (outbound) captions are trusted and pass through. The
// caller fills the cache fields (sha/size/path) from the media adapter.
func mediaRowBase(msg sqlitehistory.StoredMessage) mediaWire {
	w := mediaWire{
		MessageID: msg.MessageID,
		ChatJID:   msg.ChatJID,
		Timestamp: msg.Timestamp,
		MediaType: msg.MediaType,
	}
	switch {
	case msg.IsFromMe:
		w.Caption = msg.Caption
	case msg.Caption != "":
		chat, _ := domain.Parse(msg.ChatJID)
		sender, _ := domain.Parse(msg.SenderJID)
		w.Channel = app.ChannelWrapFields(app.InboundFields{Caption: msg.Caption}, chat, sender, msg.Timestamp)
	}
	return w
}

func makeMediaListHandler(store *sqlitehistory.Store, media *wmAdapter.MediaAdapter) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p mediaListParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, app.InvalidParams(err.Error())
			}
		}
		// Any MIME (audio/, image/, application/…) contains "/"; text rows
		// store an empty media_type. A specific --media-type narrows further.
		like := "%/%"
		if p.MediaType != "" {
			like = mediaTypeLikePattern(p.MediaType)
		}
		msgs, err := store.QueryMessagesFiltered(ctx, sqlitehistory.MessageFilter{
			ChatJID:       p.Chat,
			MediaTypeLike: like,
			Limit:         p.Limit,
		})
		if err != nil {
			return nil, err
		}
		out := make([]mediaWire, 0, len(msgs))
		for _, msg := range msgs {
			w := mediaRowBase(msg)
			info, present, ierr := media.InspectMedia(ctx, msg.MessageID)
			if ierr != nil {
				return nil, ierr
			}
			if present {
				w.SHA256 = info.SHA256
				w.Size = info.AdvertisedSize
				w.DurationSeconds = info.DurationSeconds
				w.Cached = info.Cached
				w.Path = info.Path
				w.CachedSize = info.CachedSize
			}
			out = append(out, w)
		}
		return json.Marshal(map[string]any{"media": out})
	}
}
