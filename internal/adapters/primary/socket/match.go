package socket

import "regexp"

// matchesSub reports whether evt should be delivered to sub.
// Filter DSL (FR-060): AND across distinct fields, OR within each list.
// An empty events set matches every type; zero Since matches everything.
func matchesSub(sub *Subscription, evt Event, bodyRe *regexp.Regexp) bool {
	if evt.Seq != 0 && evt.Seq <= sub.since {
		return false
	}
	if len(sub.events) > 0 {
		if _, ok := sub.events[evt.Type]; !ok {
			return false
		}
	}
	if len(sub.chats) > 0 && !containsStr(sub.chats, evt.Chat) {
		return false
	}
	if len(sub.senders) > 0 && !containsStr(sub.senders, evt.Sender) {
		return false
	}
	if len(sub.notSenders) > 0 && containsStr(sub.notSenders, evt.Sender) {
		return false
	}
	if bodyRe != nil {
		if evt.Body == "" || !bodyRe.MatchString(evt.Body) {
			return false
		}
	}
	return true
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
