package main

import (
	"encoding/json"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestRPCCodeToExit verifies the exit code mapping table in exitcodes.go
// per contracts/exit-codes.md.
func TestRPCCodeToExit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rpcCode  int
		wantExit int
	}{
		{"NotPaired", -32011, 10},
		{"NotAllowlisted", -32012, 11},
		{"RateLimited", -32013, 12},
		{"WarmupActive", -32014, 12},
		{"WaitTimeout", -32003, 12},
		{"InvalidJID", -32015, 64},
		{"MessageTooLarge", -32016, 64},
		{"InvalidParams", -32602, 64},
		{"MethodNotFound", -32601, 64},
		// Codes previously collapsing to the generic exit 1, now mapped to
		// their closest sysexits bucket (docs/manual.md §8).
		{"Disconnected", -32018, 10},
		{"SessionWiped", -32019, 10},
		{"ShutdownInProgress", -32002, 10},
		{"PolicyRefused", -32100, 11},
		{"RateLimitedHard", -32200, 12},
		{"MediaTooLarge", -32201, 64},
		{"OversizedMessage", -32004, 64},
		{"ScheduleInPast", -32112, 64},
		{"IdempotencyCollision", -32101, 64},
		{"UnsupportedMessageType", -32300, 64},
		{"EmbeddingsDisabled", -32113, 78},
		{"LabelsUnsupported", -32114, 78},
		{"TranscriberNotConfigured", -32115, 78},
		// Catalogued codes the CLI used to flatten to the generic 1.
		{"NotOnWhatsApp", -32017, 64},
		{"MessageNotFound", -32117, 64},
		{"DraftState", -32109, 64},
		{"WebhookNotFound", -32118, 64},
		{"MediaTooLargeUpload", -32111, 64},
		{"IdempotencyKeyConflict", -32108, 64},
		{"Unauthorized", -32099, 64},
		{"Backpressure", -32001, 12},
		{"ProtocolMismatch", -32008, 78},
		// Genuinely-runtime failures intentionally stay generic (1) — the
		// hint, not the exit code, carries the remediation.
		{"TranscribeFailed", -32116, 1},
		{"MediaNotCached", -32301, 1},
		{"InternalError", -32603, 1},
		{"UnknownCode", -99999, 1},
		{"ZeroCode", 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rpcCodeToExit(tt.rpcCode)
			if got != tt.wantExit {
				t.Errorf("rpcCodeToExit(%d) = %d, want %d", tt.rpcCode, got, tt.wantExit)
			}
		})
	}
}

// TestHintForRPCCode verifies that every refusal the CLI maps to a
// non-generic exit code also offers an actionable hint, and that
// unknown/internal codes stay silent (no misleading guidance).
func TestHintForRPCCode(t *testing.T) {
	t.Parallel()

	wantHint := []int{
		rpcNotPaired, rpcNotAllowlisted, rpcRateLimited, rpcRateLimitedHard,
		rpcWarmupActive, rpcInvalidJID, rpcDisconnected, rpcSessionWiped, rpcPolicyRefused,
		rpcScheduleInPast, rpcEmbeddingsDisabled, rpcLabelsUnsupported,
		rpcTranscriberNotConfigured, rpcTranscribeFailed, rpcMediaTooLarge,
		rpcUnsupportedMessageType, rpcMediaNotCached,
		rpcNotOnWhatsApp, rpcRecipientMoved, rpcMessageNotFound, rpcDraftState, rpcWebhookNotFound,
		rpcMediaTooLargeUpload, rpcIdempotencyKeyConflict, rpcUnauthorized,
		rpcBackpressure, rpcProtocolMismatch,
	}
	for _, code := range wantHint {
		if hintForRPCCode(code) == "" {
			t.Errorf("hintForRPCCode(%d) = empty, want an actionable hint", code)
		}
	}

	noHint := []int{-99999, 0, -32603 /* InternalError */, -32602 /* InvalidParams */}
	for _, code := range noHint {
		if h := hintForRPCCode(code); h != "" {
			t.Errorf("hintForRPCCode(%d) = %q, want empty", code, h)
		}
	}
}

// TestFormatResultJSON verifies that formatResult in JSON mode wraps the
// result with a versioned schema string.
func TestFormatResultJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		result     string
		wantSchema string
	}{
		{
			name:       "status",
			method:     "status",
			result:     `{"connected":true,"jid":"123@s.whatsapp.net"}`,
			wantSchema: "wa.status/v1",
		},
		{
			name:       "send",
			method:     "send",
			result:     `{"messageId":"abc123","timestamp":1234567890}`,
			wantSchema: "wa.send/v1",
		},
		{
			name:       "panic",
			method:     "panic",
			result:     `{"unlinked":true}`,
			wantSchema: "wa.panic/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := formatResult(tt.method, json.RawMessage(tt.result), true)

			var obj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &obj); err != nil {
				t.Fatalf("formatResult JSON output is not valid JSON: %v\noutput: %s", err, out)
			}

			schemaRaw, ok := obj["schema"]
			if !ok {
				t.Fatalf("JSON output missing 'schema' key: %s", out)
			}

			var schema string
			if err := json.Unmarshal(schemaRaw, &schema); err != nil {
				t.Fatalf("schema is not a string: %v", err)
			}
			if schema != tt.wantSchema {
				t.Errorf("schema = %q, want %q", schema, tt.wantSchema)
			}
		})
	}
}

// TestFormatResultHuman verifies formatResult in human mode for each
// method that has a human formatter.
func TestFormatResultHuman(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		result string
		want   string
	}{
		{
			name:   "status connected",
			method: "status",
			result: `{"connected":true,"jid":"123@s.whatsapp.net"}`,
			want:   "Connected as 123@s.whatsapp.net",
		},
		{
			name:   "status disconnected",
			method: "status",
			result: `{"connected":false}`,
			want:   "Not connected",
		},
		{
			name:   "send",
			method: "send",
			result: `{"messageId":"abc-123","timestamp":1234567890}`,
			want:   "Sent: abc-123",
		},
		{
			name:   "pair success",
			method: "pair",
			result: `{"paired":true}`,
			want:   "Paired successfully",
		},
		{
			name:   "pair failure",
			method: "pair",
			result: `{"paired":false}`,
			want:   "Pairing failed",
		},
		{
			name:   "panic unlinked",
			method: "panic",
			result: `{"unlinked":true}`,
			want:   "Device unlinked and session wiped",
		},
		{
			name:   "react",
			method: "react",
			result: `{}`,
			want:   "Reaction sent",
		},
		{
			name:   "markRead",
			method: "markRead",
			result: `{}`,
			want:   "Marked as read",
		},
		{
			name:   "groups",
			method: "groups",
			result: `{"groups":[{"jid":"g1"},{"jid":"g2"}]}`,
			want:   "2 groups",
		},
		{
			name:   "groups empty",
			method: "groups",
			result: `{"groups":[]}`,
			want:   "0 groups",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatResult(tt.method, json.RawMessage(tt.result), false)
			if got != tt.want {
				t.Errorf("formatResult(%q, ..., false) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}
