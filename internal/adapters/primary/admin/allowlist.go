package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// formActions are the per-action grants FR-116-04 offers. Any action
// the `allow` handler accepts is still listed, and any other value posted
// is refused before dispatch.
var formActions = []string{"read", "send", "group.add", "group.create"}

// allowRow is one masked allowlist entry as the list view shows it.
type allowRow struct {
	Masked  string
	Actions []string
}

// maskJID keeps the last four characters of the user part and the server
// (FR-116-04). The prefix is fixed-width so the mask leaks no length.
func maskJID(jid string) string {
	user, server, _ := strings.Cut(jid, "@")
	tail := ""
	if len(user) > 4 {
		tail = user[len(user)-4:]
	}
	return "•••" + tail + "@" + server
}

func (s *Server) allowlist(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	s.renderAllowlist(r.Context(), w, http.StatusOK, sess, "")
}

// renderAllowlist lists entries through the existing `allow` op=list
// handler. Full JIDs never reach the page: only the forms the operator
// types into carry one, and only on the request.
func (s *Server) renderAllowlist(ctx context.Context, w http.ResponseWriter, status int, sess *session, msg string) {
	var res struct {
		Rules []struct {
			JID     string   `json:"jid"`
			Actions []string `json:"actions"`
		} `json:"rules"`
	}
	raw, err := s.cfg.RPC.Handle(ctx, "allow", json.RawMessage(`{"op":"list"}`))
	if err == nil {
		err = json.Unmarshal(raw, &res)
	}
	if err != nil {
		s.cfg.Log.Error("admin allowlist list", "err", err)
		http.Error(w, "allowlist unavailable", http.StatusInternalServerError)
		return
	}
	rows := make([]allowRow, 0, len(res.Rules))
	for _, r := range res.Rules {
		rows = append(rows, allowRow{Masked: maskJID(r.JID), Actions: r.Actions})
	}
	slices.SortFunc(rows, func(a, b allowRow) int { return strings.Compare(a.Masked, b.Masked) })
	s.mu.Lock()
	incomplete := s.auditIncomplete
	s.mu.Unlock()
	s.render(w, status, "allowlist.html", map[string]any{
		"Profile": s.cfg.Profile, "CSRF": sess.csrf, "Rows": rows, "Actions": formActions,
		"Ops": []map[string]string{{"Op": "add", "Verb": "Grant"}, {"Op": "remove", "Verb": "Revoke"}}, "Error": msg, "AuditIncomplete": incomplete,
	})
}

// allowChange grants or revokes the chosen actions for one typed JID
// through the existing `allow` op handler, bracketed by the SR-116-08
// intent and outcome records.
func (s *Server) allowChange(op string, act domain.AuditAction) func(http.ResponseWriter, *http.Request, *session, string) {
	return func(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
		if !csrfOK(w, r, sess) {
			return
		}
		ctx := r.Context()
		jid, err := domain.Parse(strings.TrimSpace(r.PostFormValue("jid")))
		actions := r.PostForm["action"]
		known := len(actions) > 0
		for _, a := range actions {
			known = known && slices.Contains(formActions, a)
		}
		if err != nil || !known {
			s.renderAllowlist(ctx, w, http.StatusBadRequest, sess, "Enter a valid JID and pick at least one listed action.")
			return
		}
		if !s.intent(ctx, w, act, sess.tokenID, jid) {
			return
		}
		params, _ := json.Marshal(map[string]any{"op": op, "jid": jid.String(), "actions": actions})
		if _, err := s.cfg.RPC.Handle(ctx, "allow", params); err != nil {
			s.cfg.Log.Error("admin allowlist change failed", "op", op, "err", err)
			s.outcome(ctx, act, sess.tokenID, jid, "failed")
			s.renderAllowlist(ctx, w, http.StatusInternalServerError, sess, "The change failed and was not applied.")
			return
		}
		if !s.outcome(ctx, act, sess.tokenID, jid, "ok") {
			http.Error(w, "audit_incomplete: the allowlist changed, but the record of it failed", http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, "/admin/allowlist", http.StatusSeeOther)
	}
}
