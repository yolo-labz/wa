package whatsmeow

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"sync"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
)

// fakeWhatsmeowClient is a hand-rolled test double satisfying the
// whatsmeowClient interface. Per research §D4 the adapter uses an
// interface boundary so production tests don't need a real websocket.
//
// Tests set the Configurable fields (e.g. ConnectedFlag, SendErr) before
// handing the fake to the Adapter-under-test; after exercising, they
// inspect the Recorded fields (e.g. SentMessages) to assert behaviour.
//
// Every mutable field is guarded by mu so concurrent tests under -race
// don't flag data races.
type fakeWhatsmeowClient struct {
	mu sync.Mutex

	// Configurable state.
	ConnectedFlag bool
	LoggedInFlag  bool
	ConnectErr    error
	LogoutErr     error
	LogoutHook    func() // PR #136: lets tests inject delay/observation around Logout
	SendErr       error
	SendResp      waClient.SendResponse
	OnWhatsAppMap map[string]bool // phone(digits) → registered; nil/absent entry = registered (true)
	OnWhatsAppErr error
	PairCode      string
	PairErr       error
	QRChan        chan waClient.QRChannelItem
	QRChanErr     error
	HistorySync   *waHistorySync.HistorySync
	HistoryErr    error
	Device        *store.Device
	GroupsList    []*waTypes.GroupInfo
	GroupInfoMap  map[string]*waTypes.GroupInfo
	DeleteMediaFn func(ctx context.Context, mt waClient.MediaType, dp string, hash []byte, handle string) error
	DownloadAnyFn func(ctx context.Context, msg *waE2E.Message) ([]byte, error)
	UploadFn      func(ctx context.Context, plaintext []byte, mt waClient.MediaType) (waClient.UploadResponse, error)

	// Business / appstate.
	AppStateErr     error
	BusinessProfile *waTypes.BusinessProfile
	BusinessErr     error

	// Recorded state.
	ConnectCalls    int
	DisconnectCnt   int
	LogoutCalls     int
	SentMessages    []recordedSend
	Handlers        []waClient.EventHandlerWithSuccessStatus
	PairPhoneCall   *recordedPairPhone
	BuildHSReqs     []recordedBuildHS
	DownloadedHS    []*waE2E.HistorySyncNotification
	AppStatePatches []appstate.PatchInfo
	BusinessCalls   []waTypes.JID
	MarkReadCalls   []recordedMarkRead

	// Moderation (feature 018 T2-05).
	RevokeCalls []recordedBuildRevoke
	EditCalls   []recordedBuildEdit

	// Blocklist (feature 018 T2-09). BlocklistJIDs seeds the list returned
	// by GetBlocklist; BlocklistUpdates records every UpdateBlocklist call;
	// BlocklistErr (if set) fails both reads and writes.
	BlocklistJIDs    []waTypes.JID
	BlocklistDHash   string
	BlocklistErr     error
	BlocklistUpdates []recordedBlocklistUpdate

	// Privacy settings (feature 018 T2-11). PrivacyCurrent holds the
	// current server-side state returned by TryFetchPrivacySettings;
	// PrivacyUpdates records every SetPrivacySetting call so tests can
	// assert the (name, value) tuple reached the wire. PrivacyErr (if
	// set) fails both reads and writes. PrivacyFetchCalls counts how
	// many times the adapter consulted the server — PV4 requires this
	// to increase on every Get.
	PrivacyCurrent    waTypes.PrivacySettings
	PrivacyUpdates    []recordedPrivacyUpdate
	PrivacyErr        error
	PrivacyFetchCalls int

	// Profile edit (feature 018 T2-13). StatusMessages records every
	// SetStatusMessage call so tests can assert the exact bytes reached
	// the wire; StatusErr fails the call. AvatarInfos seeds the return
	// value for GetProfilePictureInfo keyed by (jid, preview); AvatarErr
	// fails every lookup.
	StatusMessages []string
	StatusErr      error
	AvatarInfos    map[avatarKey]*waTypes.ProfilePictureInfo
	AvatarErr      error
	AvatarCalls    []recordedAvatarCall

	// Group admin (feature 018 T2-15..T2-18). CreateGroupReqs records
	// every CreateGroup param so tests can assert the exact wire payload;
	// CreateGroupInfo seeds the returned *waTypes.GroupInfo; CreateGroupErr
	// fails the call. LeaveGroupCalls records every LeaveGroup JID;
	// LeaveGroupErr fails it.
	CreateGroupReqs []waClient.ReqCreateGroup
	CreateGroupInfo *waTypes.GroupInfo
	CreateGroupErr  error
	LeaveGroupCalls []waTypes.JID
	LeaveGroupErr   error

	// UpdateParticipantsCalls records every UpdateGroupParticipants call
	// so T2-16 tests can assert the wire action + JIDs. ParticipantsResult
	// seeds the returned slice (per-participant .Error ints); when nil the
	// fake echoes the input with no errors. ParticipantsErr fails the call
	// wholesale.
	UpdateParticipantsCalls []recordedParticipantUpdate
	ParticipantsResult      []waTypes.GroupParticipant
	ParticipantsErr         error

	// Group metadata (feature 018 T2-17). SetGroupNameCalls /
	// SetGroupTopicCalls / SetGroupPhotoCalls record every call so tests
	// can assert the wire payload. *Err seeds a failure; SetGroupPhotoID
	// seeds the returned picture ID.
	SetGroupNameCalls  []recordedGroupName
	SetGroupNameErr    error
	SetGroupTopicCalls []recordedGroupTopic
	SetGroupTopicErr   error
	SetGroupPhotoCalls []recordedGroupPhoto
	SetGroupPhotoID    string
	SetGroupPhotoErr   error

	// Group invites (feature 018 T2-18). InviteLinkCalls records every
	// GetGroupInviteLink call; InviteLinkURL seeds the returned URL;
	// InviteLinkErr fails the call. JoinLinkCalls records every
	// JoinGroupWithLink call; JoinLinkJID seeds the returned group JID;
	// JoinLinkErr fails the call.
	InviteLinkCalls []recordedInviteLink
	InviteLinkURL   string
	InviteLinkErr   error
	JoinLinkCalls   []string
	JoinLinkJID     waTypes.JID
	JoinLinkErr     error

	// LID identity resolution stubs (spec 106). The maps are read by
	// GetLIDForPN / GetPNForLID; PutLIDMapping appends to both.
	LIDForPN    map[string]waTypes.JID
	PNForLID    map[string]waTypes.JID
	LIDStoreErr error
	PutLIDCalls []recordedPutLID
}

type recordedPutLID struct {
	LID waTypes.JID
	PN  waTypes.JID
}

type recordedInviteLink struct {
	JID   waTypes.JID
	Reset bool
}

type recordedGroupName struct {
	JID  waTypes.JID
	Name string
}

type recordedGroupTopic struct {
	JID        waTypes.JID
	PreviousID string
	NewID      string
	Topic      string
}

type recordedGroupPhoto struct {
	JID    waTypes.JID
	Avatar []byte
}

type recordedParticipantUpdate struct {
	Group        waTypes.JID
	Participants []waTypes.JID
	Action       waClient.ParticipantChange
}

type avatarKey struct {
	jid     string
	preview bool
}

type recordedAvatarCall struct {
	JID     waTypes.JID
	Preview bool
}

type recordedPrivacyUpdate struct {
	Name  waTypes.PrivacySettingType
	Value waTypes.PrivacySetting
}

type recordedBlocklistUpdate struct {
	JID    waTypes.JID
	Action waEvents.BlocklistChangeAction
}

type recordedBuildRevoke struct {
	Chat   waTypes.JID
	Sender waTypes.JID
	ID     waTypes.MessageID
}

type recordedBuildEdit struct {
	Chat       waTypes.JID
	ID         waTypes.MessageID
	NewContent *waE2E.Message
}

type recordedMarkRead struct {
	IDs    []waTypes.MessageID
	Chat   waTypes.JID
	Sender waTypes.JID
	TS     time.Time
}

type recordedSend struct {
	To    waTypes.JID
	Msg   *waE2E.Message
	Extra []waClient.SendRequestExtra
}

type recordedPairPhone struct {
	Phone             string
	ShowPush          bool
	ClientType        waClient.PairClientType
	ClientDisplayName string
}

type recordedBuildHS struct {
	LastKnown *waTypes.MessageInfo
	Count     int
}

// newFakeClient returns a fake with sensible defaults: disconnected,
// logged out, an empty QR channel, no groups.
func newFakeClient() *fakeWhatsmeowClient {
	qr := make(chan waClient.QRChannelItem, 1)
	close(qr)
	return &fakeWhatsmeowClient{
		QRChan:       qr,
		GroupInfoMap: make(map[string]*waTypes.GroupInfo),
	}
}

// --- whatsmeowClient interface implementation ---

func (f *fakeWhatsmeowClient) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ConnectCalls++
	if f.ConnectErr != nil {
		return f.ConnectErr
	}
	f.ConnectedFlag = true
	return nil
}

func (f *fakeWhatsmeowClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DisconnectCnt++
	f.ConnectedFlag = false
}

func (f *fakeWhatsmeowClient) IsOnWhatsApp(_ context.Context, phones []string) ([]waTypes.IsOnWhatsAppResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.OnWhatsAppErr != nil {
		return nil, f.OnWhatsAppErr
	}
	out := make([]waTypes.IsOnWhatsAppResponse, 0, len(phones))
	for _, p := range phones {
		isIn := true
		if f.OnWhatsAppMap != nil {
			if v, ok := f.OnWhatsAppMap[p]; ok {
				isIn = v
			}
		}
		out = append(out, waTypes.IsOnWhatsAppResponse{Query: p, IsIn: isIn})
	}
	return out, nil
}

func (f *fakeWhatsmeowClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ConnectedFlag
}

func (f *fakeWhatsmeowClient) IsLoggedIn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.LoggedInFlag
}

// setLoggedIn flips LoggedInFlag under the mutex so a test goroutine can
// change it while the adapter polls IsLoggedIn() concurrently (e.g.
// BackfillRecent's login wait) without tripping the race detector.
func (f *fakeWhatsmeowClient) setLoggedIn(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LoggedInFlag = v
}

func (f *fakeWhatsmeowClient) Logout(ctx context.Context) error {
	f.mu.Lock()
	hook := f.LogoutHook
	f.LogoutCalls++
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LogoutErr != nil {
		return f.LogoutErr
	}
	f.LoggedInFlag = false
	return nil
}

func (f *fakeWhatsmeowClient) SendMessage(ctx context.Context, to waTypes.JID, message *waE2E.Message, extra ...waClient.SendRequestExtra) (waClient.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SentMessages = append(f.SentMessages, recordedSend{To: to, Msg: message, Extra: extra})
	if f.SendErr != nil {
		return waClient.SendResponse{}, f.SendErr
	}
	resp := f.SendResp
	// Mirror the real whatsmeow guarantee: a successful SendMessage
	// always returns a non-empty MessageID. Tests that don't pre-seed
	// SendResp.ID still get a deterministic synthetic id derived from
	// call index so the contract suite's MS1_happy check passes.
	if resp.ID == "" {
		resp.ID = "fake-wamid-" + strconv.Itoa(len(f.SentMessages))
	}
	if resp.Timestamp.IsZero() {
		resp.Timestamp = time.Unix(1_700_000_000, 0).UTC()
	}
	return resp, nil
}

func (f *fakeWhatsmeowClient) GetQRChannel(ctx context.Context) (<-chan waClient.QRChannelItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QRChanErr != nil {
		return nil, f.QRChanErr
	}
	return f.QRChan, nil
}

func (f *fakeWhatsmeowClient) PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType waClient.PairClientType, clientDisplayName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PairPhoneCall = &recordedPairPhone{
		Phone:             phone,
		ShowPush:          showPushNotification,
		ClientType:        clientType,
		ClientDisplayName: clientDisplayName,
	}
	if f.PairErr != nil {
		return "", f.PairErr
	}
	return f.PairCode, nil
}

func (f *fakeWhatsmeowClient) BuildHistorySyncRequest(lastKnownMessageInfo *waTypes.MessageInfo, count int) *waE2E.Message {
	// Mirror the real whatsmeow contract (send.go): the function dereferences
	// the anchor unconditionally, so a nil anchor panics. Replicating it here
	// is what lets unit tests catch a nil-anchor regression — the old
	// always-return fake silently passed the nil that crashed production
	// (PR #222 / the wa-burocracy sync panic).
	if lastKnownMessageInfo == nil {
		panic("BuildHistorySyncRequest: nil lastKnownMessageInfo (whatsmeow derefs it)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BuildHSReqs = append(f.BuildHSReqs, recordedBuildHS{LastKnown: lastKnownMessageInfo, Count: count})
	return &waE2E.Message{}
}

func (f *fakeWhatsmeowClient) DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, synchronousStorage bool) (*waHistorySync.HistorySync, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DownloadedHS = append(f.DownloadedHS, notif)
	if f.HistoryErr != nil {
		return nil, f.HistoryErr
	}
	if f.HistorySync == nil {
		return nil, errors.New("fake: no HistorySync configured")
	}
	return f.HistorySync, nil
}

func (f *fakeWhatsmeowClient) AddEventHandlerWithSuccessStatus(handler waClient.EventHandlerWithSuccessStatus) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Handlers = append(f.Handlers, handler)
	return uint32(len(f.Handlers))
}

func (f *fakeWhatsmeowClient) Store() *store.Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Device
}

func (f *fakeWhatsmeowClient) DeleteMedia(ctx context.Context, appInfo waClient.MediaType, directPath string, encFileHash []byte, encHandle string) error {
	if f.DeleteMediaFn != nil {
		return f.DeleteMediaFn(ctx, appInfo, directPath, encFileHash, encHandle)
	}
	return nil
}

func (f *fakeWhatsmeowClient) DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DownloadAnyFn != nil {
		return f.DownloadAnyFn(ctx, msg)
	}
	return nil, errors.New("fake: DownloadAny not stubbed")
}

func (f *fakeWhatsmeowClient) Upload(ctx context.Context, plaintext []byte, mt waClient.MediaType) (waClient.UploadResponse, error) {
	f.mu.Lock()
	fn := f.UploadFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, plaintext, mt)
	}
	// Default: return a deterministic synthetic response so tests that
	// don't care about bytes still exercise the code path.
	sum := sha256.Sum256(plaintext)
	return waClient.UploadResponse{
		URL:           "https://fake/upload",
		DirectPath:    "/fake/upload",
		Handle:        "fake-handle",
		ObjectID:      "fake-object",
		MediaKey:      make([]byte, 32),
		FileEncSHA256: sum[:],
		FileSHA256:    sum[:],
		FileLength:    uint64(len(plaintext)),
	}, nil
}

func (f *fakeWhatsmeowClient) MarkRead(ctx context.Context, ids []waTypes.MessageID, timestamp time.Time, chat, sender waTypes.JID, receiptTypeExtra ...waTypes.ReceiptType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MarkReadCalls = append(f.MarkReadCalls, recordedMarkRead{
		IDs:    ids,
		Chat:   chat,
		Sender: sender,
		TS:     timestamp,
	})
	return nil
}

func (f *fakeWhatsmeowClient) GetJoinedGroups(ctx context.Context) ([]*waTypes.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.GroupsList, nil
}

func (f *fakeWhatsmeowClient) GetGroupInfo(ctx context.Context, jid waTypes.JID) (*waTypes.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.GroupInfoMap[jid.String()]; ok {
		return g, nil
	}
	return nil, errors.New("fake: group not found")
}

func (f *fakeWhatsmeowClient) SendAppState(ctx context.Context, patch appstate.PatchInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AppStatePatches = append(f.AppStatePatches, patch)
	return f.AppStateErr
}

func (f *fakeWhatsmeowClient) GetBusinessProfile(ctx context.Context, jid waTypes.JID) (*waTypes.BusinessProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BusinessCalls = append(f.BusinessCalls, jid)
	if f.BusinessErr != nil {
		return nil, f.BusinessErr
	}
	return f.BusinessProfile, nil
}

func (f *fakeWhatsmeowClient) BuildRevoke(chat, sender waTypes.JID, id waTypes.MessageID) *waE2E.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RevokeCalls = append(f.RevokeCalls, recordedBuildRevoke{Chat: chat, Sender: sender, ID: id})
	return &waE2E.Message{}
}

func (f *fakeWhatsmeowClient) BuildEdit(chat waTypes.JID, id waTypes.MessageID, newContent *waE2E.Message) *waE2E.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EditCalls = append(f.EditCalls, recordedBuildEdit{Chat: chat, ID: id, NewContent: newContent})
	return &waE2E.Message{}
}

func (f *fakeWhatsmeowClient) GetBlocklist(ctx context.Context) (*waTypes.Blocklist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.BlocklistErr != nil {
		return nil, f.BlocklistErr
	}
	jids := make([]waTypes.JID, len(f.BlocklistJIDs))
	copy(jids, f.BlocklistJIDs)
	return &waTypes.Blocklist{DHash: f.BlocklistDHash, JIDs: jids}, nil
}

func (f *fakeWhatsmeowClient) UpdateBlocklist(ctx context.Context, jid waTypes.JID, action waEvents.BlocklistChangeAction) (*waTypes.Blocklist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BlocklistUpdates = append(f.BlocklistUpdates, recordedBlocklistUpdate{JID: jid, Action: action})
	if f.BlocklistErr != nil {
		return nil, f.BlocklistErr
	}
	// Mirror server-side list mutation so subsequent GetBlocklist reflects
	// the change — matches real whatsmeow's behaviour of returning the
	// updated list from UpdateBlocklist.
	switch action {
	case waEvents.BlocklistChangeActionBlock:
		found := false
		for _, existing := range f.BlocklistJIDs {
			if existing == jid {
				found = true
				break
			}
		}
		if !found {
			f.BlocklistJIDs = append(f.BlocklistJIDs, jid)
		}
	case waEvents.BlocklistChangeActionUnblock:
		out := f.BlocklistJIDs[:0]
		for _, existing := range f.BlocklistJIDs {
			if existing != jid {
				out = append(out, existing)
			}
		}
		f.BlocklistJIDs = out
	}
	jids := make([]waTypes.JID, len(f.BlocklistJIDs))
	copy(jids, f.BlocklistJIDs)
	return &waTypes.Blocklist{DHash: f.BlocklistDHash, JIDs: jids}, nil
}

func (f *fakeWhatsmeowClient) TryFetchPrivacySettings(ctx context.Context, ignoreCache bool) (*waTypes.PrivacySettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PrivacyFetchCalls++
	if f.PrivacyErr != nil {
		return nil, f.PrivacyErr
	}
	snap := f.PrivacyCurrent
	return &snap, nil
}

func (f *fakeWhatsmeowClient) SetPrivacySetting(ctx context.Context, name waTypes.PrivacySettingType, value waTypes.PrivacySetting) (waTypes.PrivacySettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PrivacyUpdates = append(f.PrivacyUpdates, recordedPrivacyUpdate{Name: name, Value: value})
	if f.PrivacyErr != nil {
		return waTypes.PrivacySettings{}, f.PrivacyErr
	}
	switch name {
	case waTypes.PrivacySettingTypeGroupAdd:
		f.PrivacyCurrent.GroupAdd = value
	case waTypes.PrivacySettingTypeLastSeen:
		f.PrivacyCurrent.LastSeen = value
	case waTypes.PrivacySettingTypeStatus:
		f.PrivacyCurrent.Status = value
	case waTypes.PrivacySettingTypeProfile:
		f.PrivacyCurrent.Profile = value
	case waTypes.PrivacySettingTypeReadReceipts:
		f.PrivacyCurrent.ReadReceipts = value
	}
	return f.PrivacyCurrent, nil
}

func (f *fakeWhatsmeowClient) SetStatusMessage(ctx context.Context, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StatusMessages = append(f.StatusMessages, msg)
	return f.StatusErr
}

func (f *fakeWhatsmeowClient) GetProfilePictureInfo(ctx context.Context, jid waTypes.JID, params *waClient.GetProfilePictureParams) (*waTypes.ProfilePictureInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	preview := params != nil && params.Preview
	f.AvatarCalls = append(f.AvatarCalls, recordedAvatarCall{JID: jid, Preview: preview})
	if f.AvatarErr != nil {
		return nil, f.AvatarErr
	}
	if f.AvatarInfos == nil {
		// Mimics upstream whatsmeow's GetProfilePictureInfo contract:
		// returns (nil, nil) when the user has no avatar / picture is
		// hidden by privacy settings. The adapter that consumes this
		// fake must handle both nil-info and ErrNotFound shapes.
		return nil, nil //nolint:nilnil // upstream contract
	}
	info, ok := f.AvatarInfos[avatarKey{jid: jid.String(), preview: preview}]
	if !ok {
		return nil, nil //nolint:nilnil // upstream contract
	}
	return info, nil
}

func (f *fakeWhatsmeowClient) CreateGroup(ctx context.Context, req waClient.ReqCreateGroup) (*waTypes.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreateGroupReqs = append(f.CreateGroupReqs, req)
	if f.CreateGroupErr != nil {
		return nil, f.CreateGroupErr
	}
	if f.CreateGroupInfo != nil {
		return f.CreateGroupInfo, nil
	}
	// Synthesise a deterministic GroupInfo so GA1 passes without the
	// caller pre-seeding every field. JID uses an index-derived counter
	// so repeated creates produce distinct group JIDs.
	jid := waTypes.NewJID(strconv.Itoa(1_600_000_000+len(f.CreateGroupReqs)), waTypes.GroupServer)
	participants := make([]waTypes.GroupParticipant, len(req.Participants))
	for i, p := range req.Participants {
		participants[i] = waTypes.GroupParticipant{JID: p}
	}
	return &waTypes.GroupInfo{
		JID:          jid,
		GroupName:    waTypes.GroupName{Name: req.Name},
		Participants: participants,
	}, nil
}

func (f *fakeWhatsmeowClient) LeaveGroup(ctx context.Context, jid waTypes.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LeaveGroupCalls = append(f.LeaveGroupCalls, jid)
	return f.LeaveGroupErr
}

func (f *fakeWhatsmeowClient) UpdateGroupParticipants(ctx context.Context, group waTypes.JID, participants []waTypes.JID, action waClient.ParticipantChange) ([]waTypes.GroupParticipant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpdateParticipantsCalls = append(f.UpdateParticipantsCalls, recordedParticipantUpdate{
		Group:        group,
		Participants: append([]waTypes.JID(nil), participants...),
		Action:       action,
	})
	if f.ParticipantsErr != nil {
		return nil, f.ParticipantsErr
	}
	if f.ParticipantsResult != nil {
		return f.ParticipantsResult, nil
	}
	// Echo the input with .Error=0 (clean success) so roster tests pass
	// without pre-seeding.
	out := make([]waTypes.GroupParticipant, len(participants))
	for i, p := range participants {
		out[i] = waTypes.GroupParticipant{JID: p}
	}
	return out, nil
}

func (f *fakeWhatsmeowClient) SetGroupName(ctx context.Context, jid waTypes.JID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetGroupNameCalls = append(f.SetGroupNameCalls, recordedGroupName{JID: jid, Name: name})
	return f.SetGroupNameErr
}

func (f *fakeWhatsmeowClient) SetGroupTopic(ctx context.Context, jid waTypes.JID, previousID, newID, topic string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetGroupTopicCalls = append(f.SetGroupTopicCalls, recordedGroupTopic{
		JID: jid, PreviousID: previousID, NewID: newID, Topic: topic,
	})
	return f.SetGroupTopicErr
}

func (f *fakeWhatsmeowClient) SetGroupPhoto(ctx context.Context, jid waTypes.JID, avatar []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetGroupPhotoCalls = append(f.SetGroupPhotoCalls, recordedGroupPhoto{
		JID: jid, Avatar: append([]byte(nil), avatar...),
	})
	if f.SetGroupPhotoErr != nil {
		return "", f.SetGroupPhotoErr
	}
	if f.SetGroupPhotoID != "" {
		return f.SetGroupPhotoID, nil
	}
	if avatar == nil {
		return "remove", nil
	}
	return "pic-id", nil
}

func (f *fakeWhatsmeowClient) GetGroupInviteLink(ctx context.Context, jid waTypes.JID, reset bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.InviteLinkCalls = append(f.InviteLinkCalls, recordedInviteLink{JID: jid, Reset: reset})
	if f.InviteLinkErr != nil {
		return "", f.InviteLinkErr
	}
	if f.InviteLinkURL != "" {
		return f.InviteLinkURL, nil
	}
	return "https://chat.whatsapp.com/AbCdEfGhIjK1234567", nil
}

func (f *fakeWhatsmeowClient) JoinGroupWithLink(ctx context.Context, code string) (waTypes.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.JoinLinkCalls = append(f.JoinLinkCalls, code)
	if f.JoinLinkErr != nil {
		return waTypes.EmptyJID, f.JoinLinkErr
	}
	if !f.JoinLinkJID.IsEmpty() {
		return f.JoinLinkJID, nil
	}
	return waTypes.NewJID("1234567890-1600000000", waTypes.GroupServer), nil
}

// GetLIDForPN looks up the LID for pn in LIDForPN. Returns the empty
// JID with nil err when no mapping is seeded — matches whatsmeow's
// real behaviour (spec 106 FR-002).
func (f *fakeWhatsmeowClient) GetLIDForPN(ctx context.Context, pn waTypes.JID) (waTypes.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LIDStoreErr != nil {
		return waTypes.EmptyJID, f.LIDStoreErr
	}
	if f.LIDForPN == nil {
		return waTypes.EmptyJID, nil
	}
	if lid, ok := f.LIDForPN[pn.String()]; ok {
		return lid, nil
	}
	return waTypes.EmptyJID, nil
}

// GetPNForLID looks up the PN for lid in PNForLID.
func (f *fakeWhatsmeowClient) GetPNForLID(ctx context.Context, lid waTypes.JID) (waTypes.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LIDStoreErr != nil {
		return waTypes.EmptyJID, f.LIDStoreErr
	}
	if f.PNForLID == nil {
		return waTypes.EmptyJID, nil
	}
	if pn, ok := f.PNForLID[lid.String()]; ok {
		return pn, nil
	}
	return waTypes.EmptyJID, nil
}

// PutLIDMapping appends to PutLIDCalls and updates both lookup maps.
func (f *fakeWhatsmeowClient) PutLIDMapping(ctx context.Context, lid, pn waTypes.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LIDStoreErr != nil {
		return f.LIDStoreErr
	}
	f.PutLIDCalls = append(f.PutLIDCalls, recordedPutLID{LID: lid, PN: pn})
	if f.LIDForPN == nil {
		f.LIDForPN = make(map[string]waTypes.JID)
	}
	if f.PNForLID == nil {
		f.PNForLID = make(map[string]waTypes.JID)
	}
	f.LIDForPN[pn.String()] = lid
	f.PNForLID[lid.String()] = pn
	return nil
}

// dispatch is a test helper that synchronously invokes every registered
// handler with the given raw event. Returns the last success status.
func (f *fakeWhatsmeowClient) dispatch(evt any) bool {
	f.mu.Lock()
	handlers := make([]waClient.EventHandlerWithSuccessStatus, len(f.Handlers))
	copy(handlers, f.Handlers)
	f.mu.Unlock()
	ok := true
	for _, h := range handlers {
		ok = h(evt)
	}
	return ok
}

// Compile-time assertion that fakeWhatsmeowClient satisfies the interface.
var _ whatsmeowClient = (*fakeWhatsmeowClient)(nil)
