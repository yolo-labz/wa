package whatsmeow

import (
	"context"
	"errors"
	"testing"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func newTestIdentityResolver(t *testing.T) (*IdentityResolverAdapter, *fakeWhatsmeowClient) {
	t.Helper()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	return a.NewIdentityResolver(), fc
}

func TestIdentityResolver_ResolveLID_Hit(t *testing.T) {
	t.Parallel()
	r, fc := newTestIdentityResolver(t)
	pnDomain := domain.MustJID("5511999999999@s.whatsapp.net")
	lidWA, _ := waTypes.ParseJID("66448177246461@lid")
	fc.LIDForPN = map[string]waTypes.JID{
		"5511999999999@s.whatsapp.net": lidWA,
	}
	got, err := r.ResolveLID(context.Background(), pnDomain)
	if err != nil {
		t.Fatalf("ResolveLID: %v", err)
	}
	want := domain.MustJID("66448177246461@lid")
	if got != want {
		t.Errorf("ResolveLID = %v, want %v", got, want)
	}
}

func TestIdentityResolver_ResolveLID_Miss(t *testing.T) {
	t.Parallel()
	r, _ := newTestIdentityResolver(t)
	pnDomain := domain.MustJID("5511999999999@s.whatsapp.net")
	got, err := r.ResolveLID(context.Background(), pnDomain)
	if err != nil {
		t.Fatalf("ResolveLID on miss: %v, want nil err (whatsmeow returns empty JID)", err)
	}
	if !got.IsZero() {
		t.Errorf("ResolveLID on miss = %v, want zero JID", got)
	}
}

func TestIdentityResolver_ResolvePN_Hit(t *testing.T) {
	t.Parallel()
	r, fc := newTestIdentityResolver(t)
	lidDomain := domain.MustJID("66448177246461@lid")
	pnWA, _ := waTypes.ParseJID("5511999999999@s.whatsapp.net")
	fc.PNForLID = map[string]waTypes.JID{
		"66448177246461@lid": pnWA,
	}
	got, err := r.ResolvePN(context.Background(), lidDomain)
	if err != nil {
		t.Fatalf("ResolvePN: %v", err)
	}
	want := domain.MustJID("5511999999999@s.whatsapp.net")
	if got != want {
		t.Errorf("ResolvePN = %v, want %v", got, want)
	}
}

func TestIdentityResolver_WrongKindRejected(t *testing.T) {
	t.Parallel()
	r, _ := newTestIdentityResolver(t)
	ctx := context.Background()
	if _, err := r.ResolveLID(ctx, domain.MustJID("66448177246461@lid")); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("ResolveLID(LID) err = %v, want ErrNotIdentity", err)
	}
	if _, err := r.ResolvePN(ctx, domain.MustJID("5511999999999@s.whatsapp.net")); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("ResolvePN(PN) err = %v, want ErrNotIdentity", err)
	}
	if err := r.RecordMapping(ctx,
		domain.MustJID("66448177246461@lid"),
		domain.MustJID("5511999999999@s.whatsapp.net"),
	); !errors.Is(err, app.ErrNotIdentity) {
		t.Errorf("RecordMapping(swapped args) err = %v, want ErrNotIdentity", err)
	}
}

func TestIdentityResolver_RecordMappingForwardsToFake(t *testing.T) {
	t.Parallel()
	r, fc := newTestIdentityResolver(t)
	pn := domain.MustJID("5511999999999@s.whatsapp.net")
	lid := domain.MustJID("66448177246461@lid")
	if err := r.RecordMapping(context.Background(), pn, lid); err != nil {
		t.Fatalf("RecordMapping: %v", err)
	}
	if got := len(fc.PutLIDCalls); got != 1 {
		t.Fatalf("PutLIDCalls = %d, want 1", got)
	}
	got, err := r.ResolveLID(context.Background(), pn)
	if err != nil {
		t.Fatalf("ResolveLID after RecordMapping: %v", err)
	}
	if got != lid {
		t.Errorf("ResolveLID after RecordMapping = %v, want %v", got, lid)
	}
}
