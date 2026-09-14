package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedFetchForwardsChatAndRejectsBeforeBytes(t *testing.T) {
	chat := "5511999999999@s.whatsapp.net"
	payload := []byte("synthetic scoped payload")
	sum := sha256.Sum256(payload)
	for _, mode := range []string{"matching", "old", "wrong-chat", "wrong-message"} {
		t.Run(mode, func(t *testing.T) {
			fd := newFakeDaemon(t)
			fd.on("media.download", func(raw json.RawMessage) (any, *rpcError) {
				var p struct {
					Chat       string `json:"chat"`
					MessageID  string `json:"messageId"`
					Transcribe bool   `json:"transcribe"`
				}
				if err := json.Unmarshal(raw, &p); err != nil || p.Chat != chat || p.MessageID != "IMAGE" || p.Transcribe {
					t.Errorf("wrong scoped RPC: %s, %v", raw, err)
				}
				result := map[string]any{"object": map[string]any{"sha256": hex.EncodeToString(sum[:])}}
				selection := map[string]string{"chatJid": chat, "messageId": "IMAGE"}
				if mode == "wrong-chat" {
					selection["chatJid"] = "5522888888888@s.whatsapp.net"
				}
				if mode == "wrong-message" {
					selection["messageId"] = "OTHER"
				}
				if mode != "old" {
					result["selection"] = selection
				}
				return result, nil
			})
			fd.on("media.fetchBytes", chunkServer(payload, 1<<20))
			out := filepath.Join(t.TempDir(), "image.bin")
			_, stderr := runCmd(t, "--socket", fd.path(), "media", "fetch", "--chat", chat,
				"--message-id", "IMAGE", "--out", out)
			if mode == "matching" {
				got, err := os.ReadFile(out)
				if err != nil || string(got) != string(payload) {
					t.Fatalf("scoped fetch: %q, %v, %s", got, err, stderr)
				}
			} else {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatalf("unbound response wrote a file: %v", err)
				}
				if !strings.Contains(stderr, "[exec error:") {
					t.Fatalf("unbound response not refused: %s", stderr)
				}
				if calls := fd.seen(); len(calls) != 1 || calls[0].Method != "media.download" {
					t.Fatalf("unbound response reached bytes: %+v", calls)
				}
			}
		})
	}
}

func TestSHAFetchCannotSilentlyIgnoreChat(t *testing.T) {
	fd := newFakeDaemon(t)
	_, stderr := runCmd(t, "--socket", fd.path(), "media", "fetch",
		"--sha256", strings.Repeat("a", 64), "--chat", "5511999999999@s.whatsapp.net")
	if !strings.Contains(stderr, "[exec error:") || len(fd.seen()) != 0 {
		t.Fatalf("SHA fetch ignored chat instead of refusing before RPC: %s, %+v", stderr, fd.seen())
	}
}

func TestValidateMediaSelectionRejectsOldOrMismatchedDaemon(t *testing.T) {
	chat := "5511999999999@s.whatsapp.net"
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"old daemon", `{"object":{}}`, "does not support"},
		{"wrong chat", `{"selection":{"chatJid":"5522888888888@s.whatsapp.net","messageId":"M1"}}`, "mismatch"},
		{"wrong message", `{"selection":{"chatJid":"5511999999999@s.whatsapp.net","messageId":"M2"}}`, "mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMediaSelection(json.RawMessage(tc.raw), chat, "M1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
	if err := validateMediaSelection(json.RawMessage(`{"selection":{"chatJid":"5511999999999@s.whatsapp.net","messageId":"M1"}}`), chat, "M1"); err != nil {
		t.Fatalf("matching scoped response: %v", err)
	}
	if err := validateMediaSelection(nil, "", "M1"); err != nil {
		t.Fatalf("legacy unqualified response must remain compatible: %v", err)
	}
}
