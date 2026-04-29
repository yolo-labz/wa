package socket

import (
	"errors"
	"fmt"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestMediaErrorsMappedToTypedCodes pins issue #102: media.download
// failures that are user-actionable (legacy schema, non-media message)
// MUST surface as -32300/-32301 — never as opaque -32603 Internal error.
func TestMediaErrorsMappedToTypedCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode ErrorCode
	}{
		{
			name:     "ErrMediaUnsupported maps to UnsupportedMessageType",
			err:      fmt.Errorf("media.download: mediaadapter: 3ADC: %w", domain.ErrMediaUnsupported),
			wantCode: CodeUnsupportedMessageType,
		},
		{
			name:     "ErrMediaNotCached maps to MediaNotCached",
			err:      fmt.Errorf("media.download: mediaadapter: 3ADC: %w", domain.ErrMediaNotCached),
			wantCode: CodeMediaNotCached,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rpc := toRPCError(tc.err)
			var je *jrpc2.Error
			if !errors.As(rpc, &je) {
				t.Fatalf("toRPCError = %T, want *jrpc2.Error", rpc)
			}
			if int(je.Code) != int(tc.wantCode) {
				t.Errorf("code = %d, want %d (%s)", je.Code, tc.wantCode, errCodeName[tc.wantCode])
			}
			if int(je.Code) == int(CodeInternalError) {
				t.Fatal("regression: typed media error fell through to -32603 Internal error")
			}
		})
	}
}
