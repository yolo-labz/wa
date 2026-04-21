package app

import (
	"encoding/json"

	"github.com/yolo-labz/wa/internal/domain"
)

// Match reports whether rec satisfies f. The evaluation is AND across
// distinct fields, OR within each list. A zero filter matches everything.
//
// BodyRe is applied to the `body` string field of the trusted JSON payload
// when present; absent / non-string body = no match when BodyRe is set.
func (f SubscribeFilter) Match(rec EventRecord) bool {
	if len(f.Kinds) > 0 && !containsString(f.Kinds, rec.Kind) {
		return false
	}
	var payload map[string]any
	if len(rec.TrustedJSON) > 0 {
		_ = json.Unmarshal(rec.TrustedJSON, &payload)
	}
	if !matchJIDField(payload, "chat", f.Chats) {
		return false
	}
	if !matchJIDField(payload, "sender", f.Senders) {
		return false
	}
	if len(f.NotSenders) > 0 {
		sender := stringField(payload, "sender")
		for _, s := range f.NotSenders {
			if sender == s.String() {
				return false
			}
		}
	}
	if f.BodyRe != nil {
		body := stringField(payload, "body")
		if body == "" || !f.BodyRe.MatchString(body) {
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func matchJIDField(payload map[string]any, field string, wanted []domain.JID) bool {
	if len(wanted) == 0 {
		return true
	}
	got := stringField(payload, field)
	for _, w := range wanted {
		if got == w.String() {
			return true
		}
	}
	return false
}

func stringField(payload map[string]any, field string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[field]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
