package socket

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/yolo-labz/wa/internal/app"
)

// TestRefused32100ShapeIdenticalToMissing asserts FR-050's existence-
// probing defeat: the JSON-RPC error envelope the socket adapter produces
// for an allowlist refusal MUST be byte-identical for any two different
// (jid, method) pairs. Leaking the JID or the refusal reason into the
// wire body would let a peer test membership by calling the dispatcher
// and comparing responses.
func TestRefused32100ShapeIdenticalToMissing(t *testing.T) {
	// Two distinct errors wrapping the same sentinel with different
	// contextual text — mirrors the way the dispatcher may surface an
	// allowlist miss from two different methods and two different JIDs.
	errA := fmt.Errorf("send to 5511999999999@s.whatsapp.net: %w", app.ErrNotAllowlisted)
	errB := fmt.Errorf("message.revoke for 5511888888888@s.whatsapp.net: %w", app.ErrNotAllowlisted)

	rpcA := toRPCError(errA)
	rpcB := toRPCError(errB)

	var jA, jB *jrpc2.Error
	if !errors.As(rpcA, &jA) {
		t.Fatalf("toRPCError(errA) = %T, want *jrpc2.Error", rpcA)
	}
	if !errors.As(rpcB, &jB) {
		t.Fatalf("toRPCError(errB) = %T, want *jrpc2.Error", rpcB)
	}

	if int(jA.Code) != int(CodePolicyRefused) {
		t.Errorf("envelope A code = %d, want %d", jA.Code, CodePolicyRefused)
	}
	if int(jB.Code) != int(CodePolicyRefused) {
		t.Errorf("envelope B code = %d, want %d", jB.Code, CodePolicyRefused)
	}

	// Marshal to JSON and compare byte-for-byte. The jrpc2.Error marshals
	// into {"code":N,"message":M} — identical bytes ⇒ no JID leakage.
	ba, err := json.Marshal(jA)
	if err != nil {
		t.Fatalf("marshal A: %v", err)
	}
	bb, err := json.Marshal(jB)
	if err != nil {
		t.Fatalf("marshal B: %v", err)
	}
	if string(ba) != string(bb) {
		t.Errorf("FR-050 wire shapes differ: A=%s B=%s", ba, bb)
	}

	// Additionally: the message field must not contain either JID.
	var envA struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(ba, &envA); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	for _, leak := range []string{"5511999999999", "5511888888888", "not allowlisted"} {
		if containsString(envA.Message, leak) {
			t.Errorf("wire message %q leaks %q — FR-050 violation", envA.Message, leak)
		}
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
