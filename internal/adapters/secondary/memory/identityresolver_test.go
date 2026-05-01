package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func TestIdentityResolver_RecordAndResolve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewIdentityResolver()

	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")

	if err := r.RecordMapping(ctx, pn, lid); err != nil {
		t.Fatalf("RecordMapping: %v", err)
	}
	got, err := r.ResolveLID(ctx, pn)
	if err != nil {
		t.Fatalf("ResolveLID: %v", err)
	}
	if got != lid {
		t.Errorf("ResolveLID = %v, want %v", got, lid)
	}
	gotPN, err := r.ResolvePN(ctx, lid)
	if err != nil {
		t.Fatalf("ResolvePN: %v", err)
	}
	if gotPN != pn {
		t.Errorf("ResolvePN = %v, want %v", gotPN, pn)
	}
}

func TestIdentityResolver_UnknownReturnsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewIdentityResolver()
	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")

	got, err := r.ResolveLID(ctx, pn)
	if err != nil {
		t.Errorf("ResolveLID on unknown PN: %v, want nil err (FR-002)", err)
	}
	if !got.IsZero() {
		t.Errorf("ResolveLID on unknown PN = %v, want zero JID (FR-002)", got)
	}
	got, err = r.ResolvePN(ctx, lid)
	if err != nil {
		t.Errorf("ResolvePN on unknown LID: %v, want nil err (FR-002)", err)
	}
	if !got.IsZero() {
		t.Errorf("ResolvePN on unknown LID = %v, want zero JID (FR-002)", got)
	}
}

func TestIdentityResolver_WrongKindRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewIdentityResolver()
	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")
	group := domain.MustJID("120363042199654321@g.us")

	if _, err := r.ResolveLID(ctx, lid); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("ResolveLID(LID) err = %v, want ErrNotIdentity", err)
	}
	if _, err := r.ResolveLID(ctx, group); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("ResolveLID(group) err = %v, want ErrNotIdentity", err)
	}
	if _, err := r.ResolvePN(ctx, pn); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("ResolvePN(PN) err = %v, want ErrNotIdentity", err)
	}
	if err := r.RecordMapping(ctx, lid, pn); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("RecordMapping(lid, pn) — args swapped — err = %v, want ErrNotIdentity", err)
	}
}

func TestIdentityResolver_RecordIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewIdentityResolver()
	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")
	for i := range 3 {
		if err := r.RecordMapping(ctx, pn, lid); err != nil {
			t.Fatalf("RecordMapping #%d: %v", i+1, err)
		}
	}
	got, _ := r.ResolveLID(ctx, pn)
	if got != lid {
		t.Errorf("post-repeat ResolveLID = %v, want %v", got, lid)
	}
}
