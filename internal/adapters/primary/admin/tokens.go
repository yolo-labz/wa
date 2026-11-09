package admin

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

const shownTime = "02/01/2006 15:04"

type tokenRow struct {
	ID, Name, Scope, Created, Expires, LastUsed, Revoked string
	Own                                                  bool
}

func when(t time.Time, zero string) string {
	if t.IsZero() {
		return zero
	}
	return t.Local().Format(shownTime)
}

func (s *Server) tokens(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	s.renderTokens(r.Context(), w, http.StatusOK, sess, "", "")
}

// renderTokens lists tokens (FR-116-05). raw is non-empty only on the
// issue POST's own response: the one place the secret is ever shown.
func (s *Server) renderTokens(ctx context.Context, w http.ResponseWriter, status int, sess *session, msg, raw string) {
	list, err := s.cfg.Tokens.List(ctx)
	if err != nil {
		s.cfg.Log.Error("admin token list", "err", err)
		http.Error(w, "token store unavailable", http.StatusInternalServerError)
		return
	}
	rows := make([]tokenRow, 0, len(list))
	for _, t := range list {
		rows = append(rows, tokenRow{
			ID: t.ID, Name: t.Name, Scope: t.Scope, Own: t.ID == sess.tokenID,
			Created: when(t.Created, ""), Expires: when(t.Expires, "never"),
			LastUsed: when(t.LastUsed, "never"), Revoked: when(t.RevokedAt, ""),
		})
	}
	s.mu.Lock()
	incomplete := s.auditIncomplete
	s.mu.Unlock()
	s.render(w, status, "tokens.html", map[string]any{
		"Profile": s.cfg.Profile, "CSRF": sess.csrf, "Rows": rows, "Error": msg, "Raw": raw, "AuditIncomplete": incomplete,
	})
}

// issueToken creates a token and shows its secret once, in this
// response only: no redirect, never logged, never audited.
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	if !csrfOK(w, r, sess) {
		return
	}
	ctx := r.Context()
	name := strings.TrimSpace(r.PostFormValue("name"))
	scope := r.PostFormValue("scope")
	days, err := strconv.Atoi(cmp.Or(r.PostFormValue("ttl_days"), "0"))
	if name == "" || len(name) > 64 || !slices.Contains([]string{"read", "send", "admin"}, scope) || err != nil || days < 0 || days > 3650 {
		s.renderTokens(ctx, w, http.StatusBadRequest, sess, "Give a name (≤64 characters), a scope, and a lifetime of 0-3650 days (0 = no expiry).", "")
		return
	}
	if !s.intent(ctx, w, domain.AuditTokenIssue, sess.tokenID, domain.JID{}) {
		return
	}
	tok, err := s.cfg.Tokens.Issue(ctx, name, scope, time.Duration(days)*24*time.Hour)
	if err != nil {
		s.cfg.Log.Error("admin token issue failed", "err", err)
		s.outcome(ctx, domain.AuditTokenIssue, sess.tokenID, domain.JID{}, "failed", "scope="+scope)
		s.renderTokens(ctx, w, http.StatusInternalServerError, sess, "The token could not be issued.", "")
		return
	}
	status := http.StatusOK
	if !s.outcome(ctx, domain.AuditTokenIssue, sess.tokenID, domain.JID{}, "ok", "id="+tok.ID+" scope="+scope) {
		// The token exists; withholding its only display would orphan it.
		status = http.StatusServiceUnavailable
	}
	s.renderTokens(ctx, w, status, sess, "", tok.Raw)
}

// revokeToken revokes a token and drops every session it backs. The
// session's own token is refused: that is "Sign out".
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	if !csrfOK(w, r, sess) {
		return
	}
	ctx := r.Context()
	id := r.PostFormValue("id")
	if id == "" || id == sess.tokenID {
		s.renderTokens(ctx, w, http.StatusBadRequest, sess, "You cannot revoke the token behind your own session; use Sign out.", "")
		return
	}
	if !s.intent(ctx, w, domain.AuditTokenRevoke, sess.tokenID, domain.JID{}) {
		return
	}
	if err := s.cfg.Tokens.Revoke(ctx, id); err != nil {
		s.cfg.Log.Info("admin token revoke failed", "token_id", id, "err", err)
		s.outcome(ctx, domain.AuditTokenRevoke, sess.tokenID, domain.JID{}, "failed", "id="+id)
		s.renderTokens(ctx, w, http.StatusBadRequest, sess, "No such active token.", "")
		return
	}
	s.dropToken(id)
	if !s.outcome(ctx, domain.AuditTokenRevoke, sess.tokenID, domain.JID{}, "ok", "id="+id) {
		http.Error(w, "audit_incomplete: the token was revoked, but the record of it failed", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/admin/tokens", http.StatusSeeOther)
}
