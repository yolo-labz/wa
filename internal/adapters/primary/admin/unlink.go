package admin

import (
	"encoding/json"
	"net/http"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// unlinkPage is FR-116-03's confirmation page. A GET changes nothing: the
// operator types the profile name and posts it with the CSRF token.
func (s *Server) unlinkPage(w http.ResponseWriter, _ *http.Request, sess *session, _ string) {
	s.renderUnlink(w, http.StatusOK, sess, "", false)
}

func (s *Server) renderUnlink(w http.ResponseWriter, status int, sess *session, msg string, done bool) {
	s.render(w, status, "unlink.html", map[string]any{
		"Profile": s.cfg.Profile, "CSRF": sess.csrf, "Key": randomID(), "Error": msg, "Done": done,
	})
}

// unlink is session.logout for this device, in R3's order: CSRF, the typed
// profile name (a mismatch is refused with no audit row and no RPC), the
// audit intent (fail closed), any pairing flow cancelled so no QR outlives
// the session, session.logout, then the outcome. The page says "unlinked"
// only when the RPC returned nil. The per-page key makes a double submit
// replay through idempotentCall rather than re-send the IQ.
func (s *Server) unlink(w http.ResponseWriter, r *http.Request, sess *session, _ string) {
	if !csrfOK(w, r, sess) {
		return
	}
	if r.PostFormValue("confirm") != s.cfg.Profile {
		s.renderUnlink(w, http.StatusBadRequest, sess, "The typed name does not match the profile; nothing was unlinked.", false)
		return
	}
	ctx := r.Context()
	if !s.intent(ctx, w, domain.AuditUnlink, sess.tokenID, domain.JID{}) {
		return
	}
	// A failed pair_cancel record has latched audit_incomplete (outcome); the
	// unlink goes ahead, and the page says the record is incomplete.
	incomplete := s.pair.cancel("unlink") != nil
	params, _ := json.Marshal(map[string]string{"idempotencyKey": r.PostFormValue("key")})
	if _, err := s.cfg.RPC.Handle(ctx, "session.logout", params); err != nil {
		s.cfg.Log.Error("admin unlink failed", "err", err)
		s.outcome(ctx, domain.AuditUnlink, sess.tokenID, domain.JID{}, "failed", "rpc_error")
		msg := "Unlinking failed and the device may be half-unlinked. Check the daemon, then run `wa pair --reset` on the host."
		if incomplete {
			msg += " The pairing cancel was not recorded (audit_incomplete)."
		}
		s.renderUnlink(w, http.StatusInternalServerError, sess, msg, false)
		return
	}
	if !s.outcome(ctx, domain.AuditUnlink, sess.tokenID, domain.JID{}, "ok", "") || incomplete {
		http.Error(w, "audit_incomplete: unlinked, but the record of it failed", http.StatusServiceUnavailable)
		return
	}
	s.renderUnlink(w, http.StatusOK, sess, "", true)
}
