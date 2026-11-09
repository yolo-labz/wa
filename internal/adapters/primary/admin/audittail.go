package admin

import (
	"net/http"
	"regexp"
)

// jidIn matches JIDs embedded in free text (actor, detail).
var jidIn = regexp.MustCompile(`[0-9A-Za-z._:-]+@(?:s\.whatsapp\.net|g\.us|lid|newsletter|broadcast|c\.us|hosted|bot)`)

func maskText(s string) string { return jidIn.ReplaceAllStringFunc(s, maskJID) }

// auditTail is FR-116-06: the last 200 records, newest first, JIDs
// masked, with the chain verdict as a banner. Rows are shown even when
// the chain is broken.
func (s *Server) auditTail(w http.ResponseWriter, _ *http.Request, _ *session, _ string) {
	data := map[string]any{"Profile": s.cfg.Profile}
	if s.cfg.AuditTail == nil {
		data["Unavailable"] = true
		s.render(w, http.StatusOK, "audit.html", data)
		return
	}
	recs, err := s.cfg.AuditTail.Tail(200)
	if err != nil {
		s.cfg.Log.Error("admin audit tail", "err", err)
		http.Error(w, "audit log unreadable", http.StatusInternalServerError)
		return
	}
	for i := range recs {
		r := &recs[i]
		r.Actor, r.Detail = maskText(r.Actor), maskText(r.Detail)
		if r.Subject != "" {
			r.Subject = maskJID(r.Subject)
		}
	}
	if verr := s.cfg.AuditTail.Verify(); verr != nil {
		data["Broken"] = verr.Error()
	}
	data["Rows"] = recs
	s.render(w, http.StatusOK, "audit.html", data)
}
