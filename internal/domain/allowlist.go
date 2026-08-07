package domain

import (
	"slices"
	"sync"
)

// actionSet is the internal per-JID permission record. Feature 018 T2-24
// swapped the original 4-field struct for a bitmap so new Action constants
// do not require a schema change here.
type actionSet struct {
	bits uint32
}

func (s actionSet) get(a Action) bool {
	if !a.IsValid() {
		return false
	}
	return s.bits&(1<<uint(a)) != 0
}

func (s *actionSet) set(a Action, v bool) {
	if !a.IsValid() {
		return
	}
	if v {
		s.bits |= 1 << uint(a)
	} else {
		s.bits &^= 1 << uint(a)
	}
}

func (s actionSet) list() []Action {
	out := make([]Action, 0)
	for _, a := range AllActions() {
		if s.get(a) {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

func (s actionSet) empty() bool {
	return s.bits == 0
}

// Allowlist is the tiered, default-deny policy for JID→Action permissions.
// It is the only pointer-receiver type in the domain because it carries a
// mutex that must not be copied. Use NewAllowlist to construct it.
type Allowlist struct {
	mu      sync.RWMutex
	entries map[JID]actionSet
}

// NewAllowlist returns an empty, default-deny Allowlist.
func NewAllowlist() *Allowlist {
	return &Allowlist{entries: make(map[JID]actionSet)}
}

// Allows reports whether the given jid is permitted to perform action.
// Unknown JIDs and invalid actions return false (default-deny).
func (a *Allowlist) Allows(jid JID, action Action) bool {
	if !action.IsValid() {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	set, ok := a.entries[jid]
	if !ok {
		return false
	}
	return set.get(action)
}

// Grant marks each action as allowed for jid. Invalid actions (including
// the zero Action) are silently ignored.
func (a *Allowlist) Grant(jid JID, actions ...Action) {
	a.mu.Lock()
	defer a.mu.Unlock()
	set := a.entries[jid]
	for _, act := range actions {
		if act.IsValid() {
			set.set(act, true)
		}
	}
	if set.empty() {
		return
	}
	a.entries[jid] = set
}

// Revoke marks each action as denied for jid. If the resulting entry is
// empty, the jid is removed from the allowlist.
func (a *Allowlist) Revoke(jid JID, actions ...Action) {
	a.mu.Lock()
	defer a.mu.Unlock()
	set, ok := a.entries[jid]
	if !ok {
		return
	}
	for _, act := range actions {
		if act.IsValid() {
			set.set(act, false)
		}
	}
	if set.empty() {
		delete(a.entries, jid)
		return
	}
	a.entries[jid] = set
}

// Entries returns a defensive copy of the current allowlist as a map from
// JID to the sorted list of granted actions. Mutating the returned map
// does not affect the Allowlist.
func (a *Allowlist) Entries() map[JID][]Action {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[JID][]Action, len(a.entries))
	for k, v := range a.entries {
		out[k] = v.list()
	}
	return out
}

// Size returns the number of JIDs with at least one granted action.
func (a *Allowlist) Size() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.entries)
}
