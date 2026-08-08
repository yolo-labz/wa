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
	Sender    string `json:"sender"`
	MediaType string `json:"mediaType"`
	// Caption is a plain substring, never a LIKE pattern: the wildcards are
	// the store's to add, so a client cannot widen its own query to `%` and
	// turn a caption filter into a full dump. Matching is accent- and
	// case-insensitive in both directions (#315).
	Caption string `json:"caption"`
	// FromMe is a three-state discriminator: nil (absent) means either
	// direction, true means own (outbound) messages only, false means
	// inbound only. A pointer, not a plain bool — a plain bool could not
	// tell "false" from "unset" and would silently filter to inbound-only
	// for every caller that forgot to send it. Matches the store's
	// MessageFilter.FromMe *bool nil-means-either contract (WAA-14).
	FromMe *bool `json:"fromMe,omitempty"`
	Since  int64 `json:"since"`
	Until  int64 `json:"until"`
	Limit  int   `json:"limit"`
}

// appliedFilterNames reports which narrowing filters the executed query
// actually carried, so a caller can prove its filter was honoured rather than
// assume it. It is derived from the MessageFilter handed to the store, not
// from the params struct, so it cannot drift into agreeing with a request
// that was never applied.
//
// encoding/json drops unknown fields, so a client newer than its daemon has
// its narrowing params silently deleted and gets the WHOLE list back with no
// error and an unchanged schema string. That is the failure this list exists
// to make loud: a --sender that silently no-ops is worse than no --sender at
// all, because the caller now believes they narrowed and fetches on that
// belief. Issue #317.
//
// mediaType is passed separately because MediaTypeLike always carries the
// "any MIME" pattern and so can never be empty.
func appliedFilterNames(f sqlitehistory.MessageFilter, mediaType string) []string {
	// Never nil: an empty request must marshal to [] and not null, so that an
	// absent field means "daemon predates this guard" and nothing else.
	applied := make([]string, 0, 7)
	for _, c := range []struct {
		name string
		on   bool
	}{
		{"chat", f.ChatJID != ""},
		{"sender", f.SenderJID != ""},
		{"mediaType", mediaType != ""},
		{"caption", f.Caption != ""},
		{"fromMe", f.FromMe != nil},
		{"since", f.Since > 0},
		{"until", f.Until > 0},
	} {
		if c.on {
			applied = append(applied, c.name)
		}
	}
	return applied
}

// mediaWire is the JSON shape for one media.list row. Size/sha/duration come
// from the proto envelope; Cached/Path/CachedSize reflect the local cache.
type mediaWire struct {
	MessageID string `json:"messageId"`
	ChatJID   string `json:"chatJid"`
	// SenderJID identifies who sent the row. It is metadata the server
	// assigns, not sender-authored content, so unlike an inbound caption it
	// is safe at the top level — and without it the only way to tell two
	// people's attachments apart in a group is a regex over the Channel
	// envelope's XML, which is what makes bulk-fetching a group by
	// timestamp proximity the path of least resistance (issue #314).
	SenderJID string `json:"senderJid,omitempty"`
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
	// PTT marks a push-to-talk voice note. Voice notes and attached audio
	// files share the audio/ogg MIME, so without this an agent listing
	// media cannot tell "someone left me a voice message" from "someone
	// forwarded me a song" short of downloading both.
	PTT bool `json:"ptt,omitempty"`
	// FromMe is the direction discriminator. NOT omitempty on purpose:
	// false (inbound) is as meaningful as true (own), and a row that
	// silently dropped the field would make an inbound row look like a
	// pre-WAA-14 daemon's. Every media.list row carries it (WAA-14).
	FromMe     bool   `json:"fromMe"`
	Cached     bool   `json:"cached"`
	Path       string `json:"path,omitempty"`
	CachedSize int64  `json:"cachedSize,omitempty"`
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
		SenderJID: msg.SenderJID,
		Timestamp: msg.Timestamp,
		MediaType: msg.MediaType,
		FromMe:    msg.IsFromMe,
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
		filter := sqlitehistory.MessageFilter{
			ChatJID:       p.Chat,
			SenderJID:     p.Sender,
			MediaTypeLike: like,
			Caption:       p.Caption,
			FromMe:        p.FromMe,
			Since:         p.Since,
			Until:         p.Until,
			Limit:         p.Limit,
		}
		msgs, err := store.QueryMessagesFiltered(ctx, filter)
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
				w.PTT = info.PTT
				w.Cached = info.Cached
				w.Path = info.Path
				w.CachedSize = info.CachedSize
			}
			out = append(out, w)
		}
		return json.Marshal(map[string]any{
			"media":          out,
			"appliedFilters": appliedFilterNames(filter, p.MediaType),
		})
	}
}
