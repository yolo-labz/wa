// adapter_porttest.go is the porttest.Adapter seed surface: the overlay
// mutators the //go:build integration contract suite uses to drive
// deterministic state without reaching into whatsmeow internals. Split
// from adapter.go (ARCH-04). Production code never calls anything in
// this file — see the "porttest.Adapter test overlay" field group on the
// Adapter struct for the consultation rules.
package whatsmeow

import (
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// deliveredIDsCap bounds the porttest-only Ack tracking set. Sized large
// enough for ES6 (1000 events) with headroom; FIFO eviction keeps memory
// flat across long-running test suites.
const deliveredIDsCap = 4096

// SeedContact inserts a contact into the overlay directory used by
// Lookup. Production code never calls this; it exists so the
// //go:build integration contract suite can drive deterministic state.
func (a *Adapter) SeedContact(c domain.Contact) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedContacts[c.JID] = c
}

// SeedGroup inserts a group into the overlay used by List/Get.
func (a *Adapter) SeedGroup(g domain.Group) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedGroups[g.JID] = g
}

// EnqueueEvent pushes an event onto the porttest-only queue.
// Bypasses eventCh (256-cap) so the contract suite's ES6 1000-event
// burst does not drop. Production code does not call this path.
func (a *Adapter) EnqueueEvent(e domain.Event) {
	a.testEvtMu.Lock()
	a.testEvtQ = append(a.testEvtQ, e)
	a.testEvtMu.Unlock()
}

// recordDelivered tracks an event id as delivered so Ack can return
// ErrUnknownEvent for ids that were never handed to the caller (ES5).
// Bounded FIFO eviction at deliveredIDsCap keeps memory flat.
func (a *Adapter) recordDelivered(id domain.EventID) {
	if id.IsZero() {
		return
	}
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	if _, ok := a.deliveredIDs[id]; ok {
		return
	}
	a.deliveredIDs[id] = struct{}{}
	a.deliveredList = append(a.deliveredList, id)
	if len(a.deliveredList) > deliveredIDsCap {
		evict := a.deliveredList[0]
		a.deliveredList = a.deliveredList[1:]
		delete(a.deliveredIDs, evict)
	}
}

// isDelivered reports whether the id was previously returned by Next.
func (a *Adapter) isDelivered(id domain.EventID) bool {
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	_, ok := a.deliveredIDs[id]
	return ok
}

// popTestEvent pulls the oldest porttest-queued event, if any.
func (a *Adapter) popTestEvent() (domain.Event, bool) {
	a.testEvtMu.Lock()
	defer a.testEvtMu.Unlock()
	if len(a.testEvtQ) == 0 {
		return nil, false
	}
	evt := a.testEvtQ[0]
	a.testEvtQ = a.testEvtQ[1:]
	return evt, true
}

// AppendHistory seeds per-chat history for HS1/HS3 contract clauses.
func (a *Adapter) AppendHistory(chat domain.JID, msg domain.Message) {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedHistory[chat] = append(a.seedHistory[chat], msg)
}

// SupportsRemoteBackfill reports whether the adapter can issue an
// on-demand BuildHistorySyncRequest. The whatsmeow adapter returns true;
// the porttest suite uses this to gate HS2.
func (a *Adapter) SupportsRemoteBackfill() bool { return true }
