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

// SendReturnDeadline is the return deadline of the send class (errata 3 to
// FR-116-16). A send that outlives ReturnDeadline is common (about 1 in 13
// personal sends, 13-17 s for media), and a caller that reads the
// {job_id, timed_out} envelope as the answer may resend: a duplicate
// message to a real person. Send-class methods therefore keep their
// synchronous shape up to this longer bound; past it the envelope stays,
// with "poll jobs.get, never resend".
//
// 45 s, not 60: Dokku/nginx's default proxy-read-timeout is 60 s, so a
// 60 s deadline raced the proxy (a 504 or a hang-up that cancels the send
// at 59.999 s, p8 M1). 45 s answers inside every default proxy window.
const SendReturnDeadline = 45 * time.Second

// IsSendClass reports whether method is in the send class (the CLI derives
// its job-following from the class table, not from a list of commands).
func IsSendClass(method string) bool { return MethodDeadlines[method].Return == SendReturnDeadline }

// boundedMaxWorker caps a promoted bounded worker. ponytail: one cap for
// all; give a method its own entry if it needs longer.
const boundedMaxWorker = 2 * time.Minute

// MethodDeadline is one MethodDeadlines entry; MaxWorker is the
// worker's context deadline.
type MethodDeadline struct {
	Class     DeadlineClass
	MaxWorker time.Duration
	// Return is how long a dispatch waits before promoting the worker:
	// ReturnDeadline, or SendReturnDeadline for the send class.
	Return time.Duration
}

// sendMethods are the bounded methods whose handler puts a message,
// reaction, edit, revoke or upload on the wire and answers with its result.
// Every other bounded method keeps ReturnDeadline. schedule.send only
// queues a row and markRead/sendSeen/composing are idempotent receipts.
var sendMethods = []string{
	"send", "sendMedia", "send.reply", "send.buttonResponse", "send.listResponse",
	"message.forward", "message.revoke", "message.edit", "react", "poll.create",
	"poll.vote", "draft.approve", "media.upload",
}

// jobMethods are the job-capable methods with their worker lifetimes:
// pair keeps its 3 min window, debug.pprof.profile its 120 s maximum.
var jobMethods = map[string]time.Duration{
	"pair":                3 * time.Minute,
	"sync.force":          10 * time.Minute,
	"media.download":      10 * time.Minute,
	"debug.pprof.profile": 3 * time.Minute,
}

// DetachedHandlers inventories the handlers whose work outlived its
// request. Each is job-class: its worker context (cancelled by jobs.cancel,
// owner revocation and shutdown) is the hook, MaxWorker its lifetime.
var DetachedHandlers = map[string]string{
	"pair": "whatsmeow Adapter.Pair derives its 3 min pairCtx from the job context",
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
	"search", "purge", "config.features", "jobs.get", "jobs.cancel",
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
		m[name] = MethodDeadline{Class: Bounded, MaxWorker: boundedMaxWorker, Return: ReturnDeadline}
	}
	for _, name := range sendMethods {
		d := m[name] // a name outside boundedMethods would be a zero entry: fail closed below
		d.Return = SendReturnDeadline
		m[name] = d
	}
	for name, life := range jobMethods {
		m[name] = MethodDeadline{Class: Job, MaxWorker: life, Return: ReturnDeadline}
	}
	return m
}()
