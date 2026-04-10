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
