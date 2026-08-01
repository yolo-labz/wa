package socket

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRPCWireMatchesSocketCodes pins app.RPCWire's wire-code literals to the
// named ErrorCode constants in this package.
//
// RPCWire cannot import socket (the socket adapter imports app, not the
// reverse), so it spells the codes as untyped literals. This test is the
// joint: if either side moves, it fails here rather than silently shipping a
// remote caller a different code than a local one. The message prefix is
// pinned too — it is what a human reads in `wa`'s stderr.
func TestRPCWireMatchesSocketCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode ErrorCode
		wantMsg  string
	}{
		{
			name:     "ErrMessageTooLarge",
			err:      fmt.Errorf("send: %w", domain.ErrMessageTooLarge),
			wantCode: CodeMediaTooLarge,
			wantMsg:  "MediaTooLarge: send: domain: message exceeds size limit",
		},
		{
			name:     "ErrIdempotencyCollision",
			err:      fmt.Errorf("send: %w", domain.ErrIdempotencyCollision),
			wantCode: CodeIdempotencyCollision,
			wantMsg:  "IdempotencyCollision: send: domain: idempotency key collision",
		},
		{
			name:     "ErrOutsideEditWindow",
			err:      fmt.Errorf("edit: %w", domain.ErrOutsideEditWindow),
			wantCode: CodePolicyRefused,
		},
		{
			name:     "ErrPastMuteTimestamp",
			err:      fmt.Errorf("mute: %w", domain.ErrPastMuteTimestamp),
			wantCode: CodePolicyRefused,
		},
		{
			name:     "ErrBlocked",
			err:      fmt.Errorf("send: %w", domain.ErrBlocked),
			wantCode: CodePolicyRefused,
		},
		{
			name:     "ErrNotAdmin",
			err:      fmt.Errorf("group.edit: %w", domain.ErrNotAdmin),
			wantCode: CodePolicyRefused,
		},
		{
			name:     "ErrEmptyGroupPatch",
			err:      fmt.Errorf("group.edit: %w", domain.ErrEmptyGroupPatch),
			wantCode: CodePolicyRefused,
		},
		{
			name:     "ErrBroadcastForbidden",
			err:      fmt.Errorf("send: %w", domain.ErrBroadcastForbidden),
			wantCode: CodePolicyRefused,
		},
		{
			// FR-050: constant body for every refused (jid, method) pair, so
			// a caller cannot probe which JIDs exist by diffing messages.
			name:     "ErrNotAllowlisted is a bare constant",
			err:      fmt.Errorf("send to 5581999@s.whatsapp.net: %w", app.ErrNotAllowlisted),
			wantCode: CodePolicyRefused,
			wantMsg:  "PolicyRefused",
		},
		{
			name:     "ErrUpstreamError",
			err:      fmt.Errorf("pin: %w", domain.ErrUpstreamError),
			wantCode: CodePeerCredRejected, // -32000, shared slot; message disambiguates
			wantMsg:  "upstream_error: pin: domain: upstream error",
		},
		{
			name:     "ErrMediaUnsupported",
			err:      fmt.Errorf("mediaadapter: 3ADC: %w", domain.ErrMediaUnsupported),
			wantCode: CodeUnsupportedMessageType,
			wantMsg:  "UnsupportedMessageType: mediaadapter: 3ADC: domain: message has no downloadable media",
		},
		{
			name:     "ErrMediaNotCached",
			err:      fmt.Errorf("mediaadapter: 3ADC: %w", domain.ErrMediaNotCached),
			wantCode: CodeMediaNotCached,
			wantMsg:  "MediaNotCached: mediaadapter: 3ADC: domain: message proto not cached",
		},
		{
			// Coded errors keep their own code and their own text — the
			// wire strings this adapter has always emitted.
			name:     "coded error keeps its code",
			err:      fmt.Errorf("media.download: %w", app.ErrMessageNotFound),
			wantCode: -32117,
			wantMsg:  "message not found",
		},
		{
			// Anything untyped stays opaque: sqlite paths and upstream
			// detail must not cross the boundary.
			name:     "untyped error is opaque",
			err:      errors.New("sqlitehistory: open /home/user/.local/share/wa/messages.db: permission denied"),
			wantCode: CodeInternalError,
			wantMsg:  "Internal error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := app.RPCWire(tc.err)
			if code != int32(tc.wantCode) {
				t.Errorf("code = %d, want %d (%s)", code, tc.wantCode, errCodeName[tc.wantCode])
			}
			if tc.wantMsg != "" && msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

// TestRPCWireNilIsNoError guards the zero case: a nil error must not be
// reported as a -32603, which would turn every success into a failure at
// any callsite that forgets to check err first.
func TestRPCWireNilIsNoError(t *testing.T) {
	if code, msg := app.RPCWire(nil); code != 0 || msg != "" {
		t.Errorf("RPCWire(nil) = (%d, %q), want (0, \"\")", code, msg)
	}
}
