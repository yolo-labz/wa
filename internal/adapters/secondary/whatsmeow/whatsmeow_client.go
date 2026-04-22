package whatsmeow

import (
	"context"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
)

// whatsmeowClient is the package-private interface the Adapter consumes.
// Production code constructs a *whatsmeow.Client (which satisfies this
// interface nominally, once the adapter wraps it); unit tests use the
// hand-rolled fakeWhatsmeowClient in whatsmeow_client_fake_test.go.
//
// Per research §D4 and contracts/whatsmeow-adapter.md §"Universal rules":
// "Adding a new *whatsmeow.Client method to the adapter MUST extend the
// interface in the same commit." This is the test boundary; any call the
// Adapter makes into whatsmeow must appear here and be fakeable.
//
// Method signatures match go.mau.fi/whatsmeow at the commit pinned by
// feature 003 commit 1 (see specs/003-whatsmeow-adapter/research.md §D11).
type whatsmeowClient interface {
	// Connection lifecycle.
	Connect() error
	Disconnect()
	IsConnected() bool
	IsLoggedIn() bool
	Logout(ctx context.Context) error

	// Messaging.
	SendMessage(ctx context.Context, to waTypes.JID, message *waE2E.Message, extra ...waClient.SendRequestExtra) (waClient.SendResponse, error)
	MarkRead(ctx context.Context, ids []waTypes.MessageID, timestamp time.Time, chat, sender waTypes.JID, receiptTypeExtra ...waTypes.ReceiptType) error

	// Moderation helpers (feature 018 T2-05). BuildRevoke constructs a
	// ProtocolMessage REVOKE that SendMessage broadcasts as a tombstone;
	// BuildEdit constructs a ProtocolMessage MESSAGE_EDIT carrying
	// newContent. Both return whatsmeow-internal *waE2E.Message values
	// that the caller must pass back to SendMessage to actually hit the
	// wire.
	BuildRevoke(chat, sender waTypes.JID, id waTypes.MessageID) *waE2E.Message
	BuildEdit(chat waTypes.JID, id waTypes.MessageID, newContent *waE2E.Message) *waE2E.Message

	// Upload encrypts and uploads the plaintext attachment bytes to
	// WhatsApp servers (feature 018 T1-08 / R-01). The caller copies the
	// response fields onto the matching waE2E.*Message protobuf.
	Upload(ctx context.Context, plaintext []byte, appInfo waClient.MediaType) (waClient.UploadResponse, error)

	// Pairing.
	GetQRChannel(ctx context.Context) (<-chan waClient.QRChannelItem, error)
	PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType waClient.PairClientType, clientDisplayName string) (string, error)

	// History sync. BuildHistorySyncRequest returns a whatsmeow-internal
	// *waE2E.Message that the caller passes back to SendMessage to ask the
	// user's phone for an on-demand history blob (research §D1).
	BuildHistorySyncRequest(lastKnownMessageInfo *waTypes.MessageInfo, count int) *waE2E.Message
	DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, synchronousStorage bool) (*waHistorySync.HistorySync, error)

	// Event delivery. AddEventHandlerWithSuccessStatus is used rather than
	// AddEventHandler because SynchronousAck=true requires the success-status
	// form so whatsmeow can decide whether to ack the upstream message only
	// after our handler reports success.
	AddEventHandlerWithSuccessStatus(handler waClient.EventHandlerWithSuccessStatus) uint32

	// Store access. The Adapter consults the device store for the paired
	// JID and for DeviceProps mutation during construction.
	Store() *store.Device

	// Media management. DeleteMedia is called on history-sync blobs the
	// adapter has chosen not to download (see Clarifications round 2).
	// DownloadAny is called by the media adapter for on-demand media.download
	// requests; it resolves the downloadable embedded in msg and returns the
	// decrypted payload. Feature 017 FR-050..FR-053.
	DeleteMedia(ctx context.Context, appInfo waClient.MediaType, directPath string, encFileHash []byte, encHandle string) error
	DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error)

	// Group metadata.
	GetJoinedGroups(ctx context.Context) ([]*waTypes.GroupInfo, error)
	GetGroupInfo(ctx context.Context, jid waTypes.JID) (*waTypes.GroupInfo, error)

	// Business labels (feature 017 T3-21, FR-120..122).
	// SendAppState pushes a LabelEdit/LabelChat/LabelMessage mutation
	// into the WA appstate pipeline. GetBusinessProfile is called once
	// at pair-time to detect whether the linked device is a Business
	// account; personal accounts return a non-nil error.
	SendAppState(ctx context.Context, patch appstate.PatchInfo) error
	GetBusinessProfile(ctx context.Context, jid waTypes.JID) (*waTypes.BusinessProfile, error)

	// Blocklist (feature 018 T2-09, FR-018/FR-019). GetBlocklist reads the
	// server-side list live (no local mirror); UpdateBlocklist sets the
	// block/unblock action for a single JID and returns the updated list.
	GetBlocklist(ctx context.Context) (*waTypes.Blocklist, error)
	UpdateBlocklist(ctx context.Context, jid waTypes.JID, action waEvents.BlocklistChangeAction) (*waTypes.Blocklist, error)

	// Privacy settings (feature 018 T2-11, FR-029/FR-030). TryFetch with
	// ignoreCache=true forces a server read so Get reflects changes made
	// from other devices. SetPrivacySetting sends the IQ and returns the
	// full updated settings struct.
	TryFetchPrivacySettings(ctx context.Context, ignoreCache bool) (*waTypes.PrivacySettings, error)
	SetPrivacySetting(ctx context.Context, name waTypes.PrivacySettingType, value waTypes.PrivacySetting) (waTypes.PrivacySettings, error)
}

// realClient wraps *whatsmeow.Client to add the Store() method signature
// the interface expects. *whatsmeow.Client already exposes a Store field,
// not a Store() method, so we provide a thin accessor. The commit-4
// constructor builds this wrapper.
type realClient struct {
	*waClient.Client
}

// Store returns the underlying *store.Device. Adapter uses it for the
// paired JID and for DeviceProps mutation.
func (r *realClient) Store() *store.Device { return r.Client.Store }
