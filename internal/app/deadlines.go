package app

import "time"

// DeadlineClass is how the dispatch boundary bounds one method
// (spec 116 FR-116-16). There is no exempt class.
type DeadlineClass int

const (
	// Bounded methods answer synchronously; one still running at
	// ReturnDeadline is promoted to a job, never waited on.
	Bounded DeadlineClass = iota + 1
	// Job methods also accept async:true; their work detaches.
	Job
)

// ReturnDeadline is the longest any dispatch waits for its worker.
const ReturnDeadline = 10 * time.Second

// boundedMaxWorker caps a promoted bounded worker. ponytail: one cap for
// all; give a method its own entry if it needs longer.
const boundedMaxWorker = 2 * time.Minute

// MethodDeadline is one MethodDeadlines entry; MaxWorker is the
// worker's context deadline.
type MethodDeadline struct {
	Class     DeadlineClass
	MaxWorker time.Duration
}

// jobMethods are the job-capable methods with their worker lifetimes:
// pair keeps its 3 min window, debug.pprof.profile its 120 s maximum.
var jobMethods = map[string]time.Duration{
	"pair":                3 * time.Minute,
	"sync.force":          10 * time.Minute,
	"media.download":      10 * time.Minute,
	"debug.pprof.profile": 3 * time.Minute,
}

// boundedMethods is every other method; a method in neither is refused.
var boundedMethods = []string{
	"status", "auth.whoami", "allow.check", "health", "messages", "history", "thread.get",
	"messages.search", "messages.list", "groups", "groups.get", "contacts.lookup",
	"contacts.search", "contacts.list", "contacts.profilePhoto", "contact.resolve",
	"contact.blocklist", "media.resolve", "media.fetchBytes", "media.list", "draft.list",
	"draft.get", "webhook.list", "webhook.deliveries", "schedule.list", "labels.list",
	"embeddings.status", "privacy.get", "export", "chat.list", "sync.status", "system.hello",
	"send", "sendMedia", "react", "markRead", "sendSeen", "send.reply", "send.buttonResponse",
	"send.listResponse", "chat.composing", "presence.composing.start", "presence.composing.stop",
	"presence.recording.start", "presence.recording.stop", "draft.approve", "draft.create",
	"webhook.replay", "draft.reject", "message.forward", "message.star",
	"message.setDisappearing", "contacts.annotate", "contacts.sync", "contacts.resolve.confirm",
	"wait", "media.gc", "poll.create", "poll.vote", "media.upload", "panic", "allow",
	"contact.block", "contact.unblock", "chat.archive", "chat.mute", "chat.pin",
	"chat.markUnread", "privacy.set", "session.logout", "session.logoutAll", "profile.setName",
	"profile.setStatus", "group.create", "group.leave", "group.addParticipants",
	"group.removeParticipants", "group.promote", "group.demote", "group.edit",
	"group.inviteGet", "group.inviteRevoke", "group.inviteJoin", "message.revoke",
	"message.edit", "schedule.send", "schedule.cancel", "schedule.update", "labels.create",
	"labels.delete", "labels.assign", "labels.unassign", "embeddings.purge", "admin.reload",
	"appstate.resync", "webhook.add", "webhook.remove", "admin.audit.rotate", "history.purge",
	"search", "purge", "config.features",
}

// uncappedMethods bypass both admission pools (coordinator ruling on the
// S4a review, F3): the kill switch and logouts must never be refused at
// saturation. They are bounded and keep the 10 s return deadline.
var uncappedMethods = map[string]bool{"panic": true, "session.logout": true, "session.logoutAll": true}

// MethodDeadlines classifies every RPC method (FR-116-16); its key set
// equals rest.MethodScopes. Byte-stream HTTP routes keep their own
// server timeouts.
var MethodDeadlines = func() map[string]MethodDeadline {
	m := make(map[string]MethodDeadline, len(boundedMethods)+len(jobMethods))
	for _, name := range boundedMethods {
		m[name] = MethodDeadline{Class: Bounded, MaxWorker: boundedMaxWorker}
	}
	for name, life := range jobMethods {
		m[name] = MethodDeadline{Class: Job, MaxWorker: life}
	}
	return m
}()
