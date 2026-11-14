package admin

import (
	"net/http"
	"regexp"
)

// jidIn matches JIDs embedded in free text (actor, detail).
var jidIn = regexp.MustCompile(`[0-9A-Za-z._:-]+@(?:s\.whatsapp\.net|g\.us|lid|newsletter|broadcast|c\.us|hosted|bot)`)

var chainLineRE = regexp.MustCompile(`^slogaudit verify: line (\d+):`)

// brokenText is the fixed banner wording: the line number and nothing else,
// so the verifier's file path and chain values never reach the page.
func brokenText(err error) string {
	if m := chainLineRE.FindStringSubmatch(err.Error()); m != nil {
		return "Audit chain broken at line " + m[1]
	}
	return "Audit chain could not be verified"
}

func maskText(s string) string { return jidIn.ReplaceAllStringFunc(s, MaskJID) }

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
			r.Subject = MaskJID(r.Subject)
		}
	}
	if verr := s.cfg.AuditTail.Verify(); verr != nil {
		data["Broken"] = brokenText(verr)
	}
	data["Rows"] = recs
	s.render(w, http.StatusOK, "audit.html", data)
}
