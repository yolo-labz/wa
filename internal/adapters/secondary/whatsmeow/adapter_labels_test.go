package whatsmeow

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// waBusinessProfileFake is a minimal non-nil BusinessProfile used to drive
// DetectBusiness's "business" branch.
var waBusinessProfileFake = waTypes.BusinessProfile{}

// fakeDeviceWithJID returns a *store.Device with only ID populated — enough
// for DetectBusiness to call GetBusinessProfile on the embedded JID.
func fakeDeviceWithJID(t *testing.T, jidStr string) *store.Device {
	t.Helper()
	j, err := waTypes.ParseJID(jidStr)
	if err != nil {
		t.Fatalf("ParseJID(%q): %v", jidStr, err)
	}
	return &store.Device{ID: &j}
}

// TestLabelsAdapter_SatisfiesContract drives the LabelManager contract
// suite (LM1..LM8) against the whatsmeow adapter with a fake client. The
// contract is adapter-agnostic — both the in-memory fake and the
// whatsmeow-backed adapter must pass.
func TestLabelsAdapter_SatisfiesContract(t *testing.T) {
	porttest.RunLabelManagerContract(t, func(t *testing.T) app.LabelManager {
		fake := newFakeClient()
		a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		})
		t.Cleanup(func() { _ = a.Close() })
		return a.NewLabelsAdapter(nil)
	})
}

// TestLabelsAdapter_CreateSendsLabelEdit asserts that CreateLabel pushes a
// BuildLabelEdit PatchInfo through SendAppState with deleted=false.
func TestLabelsAdapter_CreateSendsLabelEdit(t *testing.T) {
	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	if _, err := la.CreateLabel(context.Background(), "default", "Work", 3); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if len(fake.AppStatePatches) != 1 {
		t.Fatalf("AppStatePatches len = %d, want 1", len(fake.AppStatePatches))
	}
	if fake.AppStatePatches[0].Type != appstate.WAPatchRegular {
		t.Fatalf("patch type = %v, want WAPatchRegular", fake.AppStatePatches[0].Type)
	}
	if len(fake.AppStatePatches[0].Mutations) != 1 {
		t.Fatalf("patch mutations len = %d, want 1", len(fake.AppStatePatches[0].Mutations))
	}
	m := fake.AppStatePatches[0].Mutations[0]
	if m.Value == nil || m.Value.LabelEditAction == nil {
		t.Fatalf("patch value missing LabelEditAction: %+v", m.Value)
	}
	if got := m.Value.LabelEditAction.GetDeleted(); got {
		t.Fatalf("LabelEditAction.Deleted = true, want false")
	}
	if got := m.Value.LabelEditAction.GetName(); got != "Work" {
		t.Fatalf("LabelEditAction.Name = %q, want Work", got)
	}
}

// TestLabelsAdapter_DeleteSendsDeleteFlag asserts DeleteLabel sets
// Deleted=true on the underlying LabelEditAction.
func TestLabelsAdapter_DeleteSendsDeleteFlag(t *testing.T) {
	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	lbl, err := la.CreateLabel(context.Background(), "default", "Work", 1)
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if err := la.DeleteLabel(context.Background(), "default", lbl.ID()); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}
	// Last patch is the delete.
	last := fake.AppStatePatches[len(fake.AppStatePatches)-1]
	edit := last.Mutations[0].Value.GetLabelEditAction()
	if edit == nil || !edit.GetDeleted() {
		t.Fatalf("delete patch did not set Deleted=true: %+v", edit)
	}
}

// TestLabelsAdapter_AssignChatPatch asserts chat assignments use
// BuildLabelChat (LabelAssociationAction without messageID).
func TestLabelsAdapter_AssignChatPatch(t *testing.T) {
	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	lbl, _ := la.CreateLabel(context.Background(), "default", "Work", 1)
	chat, _ := domain.Parse("5511999999999@s.whatsapp.net")
	assignment, _ := domain.NewChatLabelAssignment(lbl.ID(), chat, time.Now())
	if err := la.Assign(context.Background(), "default", assignment); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	last := fake.AppStatePatches[len(fake.AppStatePatches)-1]
	assoc := last.Mutations[0].Value.GetLabelAssociationAction()
	if assoc == nil {
		t.Fatalf("assign patch missing LabelAssociationAction")
	}
	if !assoc.GetLabeled() {
		t.Fatalf("assign patch Labeled = false, want true")
	}
	// Chat index has shape [IndexLabelAssociationChat, labelID, chatJID, "0"].
	idx := last.Mutations[0].Index
	if len(idx) < 3 || idx[0] != appstate.IndexLabelAssociationChat {
		t.Fatalf("assign index = %v, want first element %q", idx, appstate.IndexLabelAssociationChat)
	}
}

// TestLabelsAdapter_AssignMessagePatch asserts message assignments use
// BuildLabelMessage (includes message id in the mutation index).
func TestLabelsAdapter_AssignMessagePatch(t *testing.T) {
	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	lbl, _ := la.CreateLabel(context.Background(), "default", "Work", 1)
	chat, _ := domain.Parse("5511999999999@s.whatsapp.net")
	assignment, _ := domain.NewMessageLabelAssignment(lbl.ID(), chat, domain.MessageID("msg_42"), time.Now())
	if err := la.Assign(context.Background(), "default", assignment); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	last := fake.AppStatePatches[len(fake.AppStatePatches)-1]
	idx := last.Mutations[0].Index
	if len(idx) < 4 || idx[0] != appstate.IndexLabelAssociationMessage {
		t.Fatalf("assign index = %v, want first element %q", idx, appstate.IndexLabelAssociationMessage)
	}
	// Message id appears in the index per the whatsmeow shape.
	found := false
	for _, tok := range idx {
		if tok == "msg_42" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("message id missing from mutation index: %v", idx)
	}
}

// TestLabelsAdapter_UnassignLabeledFalse asserts Unassign flips Labeled to
// false on the LabelAssociationAction.
func TestLabelsAdapter_UnassignLabeledFalse(t *testing.T) {
	fake := newFakeClient()
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	lbl, _ := la.CreateLabel(context.Background(), "default", "Work", 1)
	chat, _ := domain.Parse("5511999999999@s.whatsapp.net")
	assignment, _ := domain.NewChatLabelAssignment(lbl.ID(), chat, time.Now())
	_ = la.Assign(context.Background(), "default", assignment)
	if err := la.Unassign(context.Background(), "default", assignment); err != nil {
		t.Fatalf("Unassign: %v", err)
	}
	last := fake.AppStatePatches[len(fake.AppStatePatches)-1]
	assoc := last.Mutations[0].Value.GetLabelAssociationAction()
	if assoc == nil || assoc.GetLabeled() {
		t.Fatalf("unassign patch Labeled = true, want false")
	}
}

// TestAdapter_DetectBusiness_PersonalReturnsFalse asserts that a personal
// account (whose GetBusinessProfile returns an ElementMissingError) maps
// to (false, nil) so callers don't treat a lack of profile as a fatal.
func TestAdapter_DetectBusiness_PersonalReturnsFalse(t *testing.T) {
	fake := newFakeClient()
	// Seed a paired device so DetectBusiness doesn't short-circuit on
	// "device not paired".
	dev := fakeDeviceWithJID(t, "5511999999999@s.whatsapp.net")
	fake.Device = dev
	fake.BusinessErr = errors.New("business_profile missing in response to business profile query")

	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })

	got, err := a.DetectBusiness(context.Background())
	if err != nil {
		t.Fatalf("DetectBusiness: unexpected err %v", err)
	}
	if got {
		t.Fatalf("DetectBusiness = true, want false for personal account")
	}
}

// TestAdapter_DetectBusiness_BusinessReturnsTrue asserts that a non-nil
// BusinessProfile collapses to true.
func TestAdapter_DetectBusiness_BusinessReturnsTrue(t *testing.T) {
	fake := newFakeClient()
	dev := fakeDeviceWithJID(t, "5511999999999@s.whatsapp.net")
	fake.Device = dev
	fake.BusinessProfile = &waBusinessProfileFake

	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })

	got, err := a.DetectBusiness(context.Background())
	if err != nil {
		t.Fatalf("DetectBusiness: unexpected err %v", err)
	}
	if !got {
		t.Fatalf("DetectBusiness = false, want true for business account")
	}
}

// TestLabelsAdapter_SendAppStateErrorPropagates asserts that a failure from
// SendAppState does not poison the local cache.
func TestLabelsAdapter_SendAppStateErrorPropagates(t *testing.T) {
	fake := newFakeClient()
	sentinel := errors.New("fake: appstate offline")
	fake.AppStateErr = sentinel
	a := openWithClient(fake, domain.NewAllowlist(), slog.Default(), nil)
	t.Cleanup(func() { _ = a.Close() })
	la := a.NewLabelsAdapter(nil)

	_, err := la.CreateLabel(context.Background(), "default", "Work", 1)
	if !errors.Is(err, sentinel) {
		t.Fatalf("CreateLabel err = %v, want sentinel", err)
	}
	got, _ := la.ListLabels(context.Background(), "default")
	if len(got) != 0 {
		t.Fatalf("ListLabels len = %d, want 0 (cache must not be dirtied on send failure)", len(got))
	}
}
