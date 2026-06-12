package domain

import "testing"

func TestProtoVersionConst(t *testing.T) {
	t.Parallel()
	if ProtoVersion != 2 {
		t.Fatalf("ProtoVersion = %d, want 2 (FR-012 freeze)", ProtoVersion)
	}
}
