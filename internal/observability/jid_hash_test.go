package observability

import (
	"strings"
	"testing"
)

// TestJIDHashedNotLeaked asserts the HashJID privacy contract:
//   - output never contains the raw JID substring;
//   - empty input returns empty string (caller convenience);
//   - same JID → same 16-hex within a process (traces correlate);
//   - different JIDs → different hashes (basic collision resistance).
func TestJIDHashedNotLeaked(t *testing.T) {
	resetJIDSaltForTest()
	jid := "5511999999999@s.whatsapp.net"
	h := HashJID(jid)

	if h == "" {
		t.Fatal("HashJID(nonempty) = empty")
	}
	if len(h) != 16 {
		t.Errorf("HashJID len = %d, want 16 hex chars", len(h))
	}
	if strings.Contains(h, "5511") {
		t.Errorf("HashJID = %q leaks digits from input %q", h, jid)
	}
	if strings.Contains(h, "whatsapp") {
		t.Errorf("HashJID = %q leaks domain from input %q", h, jid)
	}
	if HashJID("") != "" {
		t.Errorf("HashJID(\"\") = %q, want empty", HashJID(""))
	}

	// Stability within a process.
	if HashJID(jid) != h {
		t.Error("HashJID not stable for same input within a process")
	}

	// Distinct JIDs → distinct hashes.
	other := HashJID("5511888888888@s.whatsapp.net")
	if other == h {
		t.Error("HashJID collides on distinct JIDs")
	}
}

// TestJIDHashSaltRandomizesAcrossProcesses asserts resetJIDSaltForTest
// rerolls the salt so two "process generations" within one test
// produce different hashes for the same JID. This pins the behaviour
// that across restarts an operator cannot correlate JIDs by hash.
func TestJIDHashSaltRandomizesAcrossProcesses(t *testing.T) {
	jid := "5511999999999@s.whatsapp.net"

	resetJIDSaltForTest()
	first := HashJID(jid)

	resetJIDSaltForTest()
	second := HashJID(jid)

	if first == second {
		t.Error("HashJID produced identical output after salt reset — salt was not rerolled")
	}
}
