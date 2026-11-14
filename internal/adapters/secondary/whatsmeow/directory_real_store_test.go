package whatsmeow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	waTypes "go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitestore"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// realContactAdapter backs the adapter's Store() with a real sqlstore
// contact table (synthetic 1555 identities only).
func realContactAdapter(t *testing.T) *Adapter {
	t.Helper()
	ctx := context.Background()
	st, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "s.db"), waLog.Noop)
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	own := waTypes.NewADJID("15550100000", 0, 1)
	dev := st.Container().NewDevice()
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{1}, AccountSignature: make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	if err := dev.Save(ctx); err != nil {
		t.Fatalf("dev.Save: %v", err)
	}
	for _, j := range []waTypes.JID{
		waTypes.NewJID("15550100001", waTypes.DefaultUserServer),
		waTypes.NewJID("15550100002", waTypes.HiddenUserServer),
	} {
		if _, _, err := dev.Contacts.PutPushName(ctx, j, "synthetic name"); err != nil {
			t.Fatalf("PutPushName: %v", err)
		}
	}
	fc := newFakeClient()
	fc.Device = dev
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestLookup_RealContactStore(t *testing.T) {
	a := realContactAdapter(t)
	ctx := context.Background()
	for _, s := range []string{"15550100001@s.whatsapp.net", "15550100002@lid"} {
		j, _ := domain.Parse(s)
		c, err := a.Lookup(ctx, j)
		if err != nil || c.JID != j || c.PushName != "synthetic name" {
			t.Errorf("Lookup(%s) = %+v, %v; want the stored contact", s, c, err)
		}
	}
	for _, s := range []string{"15550199999@s.whatsapp.net", "15550199999@newsletter"} {
		j, _ := domain.Parse(s)
		if _, err := a.Lookup(ctx, j); !errors.Is(err, app.ErrContactNotFound) {
			t.Errorf("Lookup(%s) err = %v; want app.ErrContactNotFound", s, err)
		}
	}
}
