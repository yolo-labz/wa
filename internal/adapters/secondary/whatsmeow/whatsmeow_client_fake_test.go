package whatsmeow

import (
	"context"
	"errors"
	"sync"

	waClient "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"
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
	SendErr       error
	SendResp      waClient.SendResponse
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

	// Recorded state.
	ConnectCalls  int
	DisconnectCnt int
	LogoutCalls   int
	SentMessages  []recordedSend
	Handlers      []waClient.EventHandlerWithSuccessStatus
	PairPhoneCall *recordedPairPhone
	BuildHSReqs   []recordedBuildHS
	DownloadedHS  []*waE2E.HistorySyncNotification
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

func (f *fakeWhatsmeowClient) Logout(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LogoutCalls++
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
	return f.SendResp, nil
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
