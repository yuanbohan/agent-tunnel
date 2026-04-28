package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendJSON(any) error { return nil }
func (fakeAgentPeer) Close() error       { return nil }

type recordingPeer struct {
	mu     sync.Mutex
	frames []protocol.AgentFrame
	closed int
}

type blockingPeer struct {
	started sync.Once
	ready   chan struct{}
	release chan struct{}
}

type deactivatablePeer struct {
	mu     sync.Mutex
	active bool
}

type disconnectingStopPeer struct {
	reg       *Registry
	sessionID string
}

func (p *recordingPeer) SendJSON(msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch typed := msg.(type) {
	case protocol.AgentFrame:
		p.frames = append(p.frames, typed)
	default:
		if frame, ok := typed.(*protocol.AgentFrame); ok && frame != nil {
			p.frames = append(p.frames, *frame)
		}
	}
	return nil
}

func (p *recordingPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed++
	return nil
}

func (p *blockingPeer) SendJSON(any) error {
	p.started.Do(func() {
		close(p.ready)
	})
	<-p.release
	return nil
}

func (p *blockingPeer) Close() error { return nil }

func (p *deactivatablePeer) SendJSON(any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return ErrAgentPeerInactive
	}
	return nil
}

func (p *deactivatablePeer) Close() error { return nil }

func (p *deactivatablePeer) Deactivate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
}

func (p *disconnectingStopPeer) SendJSON(any) error {
	p.reg.DisconnectIfOwner(p.sessionID, p)
	return nil
}

func (p *disconnectingStopPeer) Close() error { return nil }

func (p *recordingPeer) Frames() []protocol.AgentFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.AgentFrame(nil), p.frames...)
}

type recordingAttachPeer struct {
	mu          sync.Mutex
	controls    []protocol.AttachControlMessage
	binaries    [][]byte
	closeReason []string
}

type failingAttachPeer struct {
	mu            sync.Mutex
	failControlAt int
	failBinaryAt  int
	controlCalls  int
	binaryCalls   int
	closeReason   []string
}

func (p *recordingAttachPeer) SendControl(msg protocol.AttachControlMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.controls = append(p.controls, msg)
	return nil
}

func (p *recordingAttachPeer) SendBinary(payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binaries = append(p.binaries, append([]byte(nil), payload...))
	return nil
}

func (p *recordingAttachPeer) Close(reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeReason = append(p.closeReason, reason)
	return nil
}

func (p *failingAttachPeer) SendControl(msg protocol.AttachControlMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.controlCalls++
	if p.failControlAt > 0 && p.controlCalls == p.failControlAt {
		return errors.New("control write failed")
	}
	return nil
}

func (p *failingAttachPeer) SendBinary(payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binaryCalls++
	if p.failBinaryAt > 0 && p.binaryCalls == p.failBinaryAt {
		return errors.New("binary write failed")
	}
	return nil
}

func (p *failingAttachPeer) Close(reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeReason = append(p.closeReason, reason)
	return nil
}

func (p *failingAttachPeer) CloseReasons() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.closeReason...)
}

func (p *recordingAttachPeer) Controls() []protocol.AttachControlMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.AttachControlMessage(nil), p.controls...)
}

func (p *recordingAttachPeer) Binaries() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.binaries...)
}

func (p *recordingAttachPeer) CloseReasons() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.closeReason...)
}

func TestRegistryRegisterAndListSortedByStartedAt(t *testing.T) {
	reg := NewRegistry()
	older := protocol.SessionInfo{
		SessionID: "a",
		Launcher:  "codex",
		StartedAt: 10,
	}
	newer := protocol.SessionInfo{
		SessionID: "b",
		Launcher:  "gemini",
		StartedAt: 11,
	}

	reg.Register(older, fakeAgentPeer{})
	reg.Register(newer, fakeAgentPeer{})

	got := reg.List()
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(got))
	}
	if got[0].SessionID != "b" || got[1].SessionID != "a" {
		t.Fatalf("order = %#v, want b before a", got)
	}
}

func TestRegistryListForUserReturnsOnlyOwnedSessions(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "alice-newer",
		Launcher:  "codex",
		StartedAt: 20,
	}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, fakeAgentPeer{})
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "bob",
		Launcher:  "codex",
		StartedAt: 30,
	}, SessionOwner{UserID: 2, AgentTokenID: "agt-2"}, fakeAgentPeer{})
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "alice-older",
		Launcher:  "codex",
		StartedAt: 10,
	}, SessionOwner{UserID: 1, AgentTokenID: "agt-3"}, fakeAgentPeer{})

	got := reg.ListForUser(1)
	if len(got) != 2 {
		t.Fatalf("len(ListForUser) = %d, want 2", len(got))
	}
	if got[0].SessionID != "alice-newer" || got[1].SessionID != "alice-older" {
		t.Fatalf("sessions = %#v, want alice-newer then alice-older", got)
	}
}

func TestRegistryStopForUserSendsStopAndRemovesSession(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, owner)
	if _, err := reg.StartAttach("sess-1", "client-1", client); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	if err := reg.StopForUser("sess-1", 2); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-user stop error = %v, want ErrSessionNotFound", err)
	}
	if err := reg.StopForUser("sess-1", 1); err != nil {
		t.Fatalf("StopForUser returned error: %v", err)
	}
	if _, ok := reg.Session("sess-1"); ok {
		t.Fatal("session still exists after StopForUser")
	}
	frames := owner.Frames()
	if len(frames) != 1 || frames[0].Type != "stop_session" {
		t.Fatalf("frames = %#v, want one stop_session", frames)
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_stopped" {
		t.Fatalf("close reasons = %#v, want [session_stopped]", reasons)
	}
}

func TestRegistryStopForUserReturnsSuccessWhenAgentDisconnectsAfterStopDelivery(t *testing.T) {
	reg := NewRegistry()
	owner := &disconnectingStopPeer{reg: reg, sessionID: "sess-1"}
	client := &recordingAttachPeer{}
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, owner)
	if _, err := reg.StartAttach("sess-1", "client-1", client); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	if err := reg.StopForUser("sess-1", 1); err != nil {
		t.Fatalf("StopForUser returned error: %v, want nil after delivered stop", err)
	}
	if _, ok := reg.Session("sess-1"); ok {
		t.Fatal("session still exists after stop/disconnect")
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_stopped" {
		t.Fatalf("close reasons = %#v, want [session_stopped]", reasons)
	}
}

func TestRegistrySetLaunchSourceForUserBackfillsLiveSession(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "codex",
		LaunchSource: protocol.SessionLaunchSourceLocal,
	}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, fakeAgentPeer{})

	if ok := reg.SetLaunchSourceForUser("sess-1", 2, protocol.SessionLaunchSourceMobile); ok {
		t.Fatal("SetLaunchSourceForUser returned true for cross-user session")
	}
	if ok := reg.SetLaunchSourceForUser("sess-1", 1, protocol.SessionLaunchSourceMobile); !ok {
		t.Fatal("SetLaunchSourceForUser returned false for owned session")
	}
	info, ok := reg.SessionForUser("sess-1", 1)
	if !ok {
		t.Fatal("SessionForUser returned false")
	}
	if info.LaunchSource != protocol.SessionLaunchSourceMobile {
		t.Fatalf("LaunchSource = %q, want mobile", info.LaunchSource)
	}
}

func TestRegistryRemoveForUserRemovesOwnedSessionAndClosesAttaches(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, SessionOwner{UserID: 1}, owner)
	if _, err := reg.StartAttach("sess-1", "client-1", client); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	if removed := reg.RemoveForUser("sess-1", 2); removed {
		t.Fatal("RemoveForUser returned true for cross-user session")
	}
	if _, ok := reg.Session("sess-1"); !ok {
		t.Fatal("session missing after cross-user RemoveForUser")
	}
	if removed := reg.RemoveForUser("sess-1", 1); !removed {
		t.Fatal("RemoveForUser returned false for owner")
	}
	if _, ok := reg.Session("sess-1"); ok {
		t.Fatal("session still exists after owner RemoveForUser")
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_offline" {
		t.Fatalf("close reasons = %#v, want [session_offline]", reasons)
	}
	if owner.closed != 0 {
		t.Fatalf("owner closed = %d, want 0", owner.closed)
	}
}

func TestRegistryMissingSessionErrors(t *testing.T) {
	reg := NewRegistry()

	if err := reg.WriteAttachInput("missing", protocol.ForwardInputTextFrame("", "x", false)); err != ErrSessionNotFound {
		t.Fatalf("WriteAttachInput error = %v, want ErrSessionNotFound", err)
	}
}

func TestRegistryReplaceSessionIDLogsSessionReplaced(t *testing.T) {
	reg := NewRegistry()
	logs := &bytes.Buffer{}
	restore := logx.UseWriterForTest(logs)
	defer restore()

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})

	entries := parseRegistryLogEntries(t, logs.Bytes())
	if len(entries) != 1 {
		t.Fatalf("log entry count = %d, want 1", len(entries))
	}
	if got := entries[0]["event"]; got != "session_replaced" {
		t.Fatalf("event = %v, want session_replaced", got)
	}
}

func TestRegistryDisconnectRemovesSessionAndClosesAttaches(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	attachPeer := &recordingAttachPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	if _, err := reg.StartAttach("sess-1", "client-1", attachPeer); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	if _, ok := reg.Session("sess-1"); ok {
		t.Fatal("Session returned true after disconnect, want false")
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(got))
	}
	if reasons := attachPeer.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_offline" {
		t.Fatalf("close reasons = %#v, want [session_offline]", reasons)
	}
}

func TestRegistryReconnectAfterDisconnectRegistersNewOnlineSession(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
		StartedAt: 10,
	}, oldPeer)

	if disconnected := reg.DisconnectIfOwner("sess-1", oldPeer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	newPeer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
		StartedAt: 10,
	}, newPeer)

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false after reconnect, want true")
	}
	if info.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", info.SessionID)
	}
}

func TestRegistryDisconnectRemovesSessionImmediately(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	if _, ok := reg.Session("sess-1"); ok {
		t.Fatal("Session returned true after disconnect, want false")
	}
}

func TestRegistryDisconnectSkipsStaleOwnerAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

	if disconnected := reg.DisconnectIfOwner("sess-1", oldPeer); disconnected {
		t.Fatal("DisconnectIfOwner returned true for stale owner, want false")
	}

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false, want true")
	}
	if info.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", info.SessionID)
	}
	if err := reg.WriteAttachInput("sess-1", protocol.ForwardInputTextFrame("", "ping", false)); err != nil {
		t.Fatalf("WriteAttachInput returned error after stale-owner disconnect: %v", err)
	}
	frames := newPeer.Frames()
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if got := frames[0]; got.Type != "input_text" || got.Text != "ping" || got.Submit {
		t.Fatalf("new peer input = %#v, want input_text ping submit=false", got)
	}
}

func TestRegistryReplacementDeactivatesStaleOwnerPeer(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &deactivatablePeer{active: true}
	client := &recordingAttachPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)

	startedOwner, err := reg.StartAttach("sess-1", "client-1", client)
	if err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})

	if err := startedOwner.SendJSON(protocol.AttachOpenFrame("client-1")); !errors.Is(err, ErrAgentPeerInactive) {
		t.Fatalf("stale owner SendJSON error = %v, want ErrAgentPeerInactive", err)
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_offline" {
		t.Fatalf("close reasons = %#v, want [session_offline]", reasons)
	}
}

func TestRegistryWriteAttachInputReturnsNotFoundForDisconnectedSession(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	reg.DisconnectIfOwner("sess-1", peer)

	err := reg.WriteAttachInput("sess-1", protocol.ForwardInputTextFrame("", "ping", false))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WriteAttachInput error = %v, want ErrSessionNotFound", err)
	}
	if frames := peer.Frames(); len(frames) != 0 {
		t.Fatalf("frames = %#v, want none", frames)
	}
}

func TestRegistryDisconnectDoesNotWaitForBlockedSend(t *testing.T) {
	reg := NewRegistry()
	peer := &blockingPeer{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- reg.WriteAttachInput("sess-1", protocol.ForwardInputTextFrame("", "ping", false))
	}()

	select {
	case <-peer.ready:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for blocked send to start")
	}

	disconnectDone := make(chan bool, 1)
	go func() {
		disconnectDone <- reg.DisconnectIfOwner("sess-1", peer)
	}()

	select {
	case disconnected := <-disconnectDone:
		if !disconnected {
			t.Fatal("DisconnectIfOwner returned false, want true")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("DisconnectIfOwner blocked behind SendToOwner")
	}

	close(peer.release)

	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("WriteAttachInput returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked send to finish")
	}
}

func TestRegistryRoutesAttachLifecycle(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, owner)

	startedOwner, err := reg.StartAttach("sess-1", "client-1", client)
	if err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}
	if startedOwner != owner {
		t.Fatalf("owner = %#v, want recording peer", startedOwner)
	}
	if ok := reg.RouteResizeIfOwner("sess-1", owner, 100, 30); !ok {
		t.Fatal("RouteResizeIfOwner before attach_ready returned false, want true")
	}
	if controls := client.Controls(); len(controls) != 0 {
		t.Fatalf("controls before attach_ready = %#v, want none", controls)
	}

	if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); !ok {
		t.Fatal("RouteAttachReadyIfOwner returned false, want true")
	}
	if ok := reg.RouteResizeIfOwner("sess-1", owner, 121, 41); !ok {
		t.Fatal("RouteResizeIfOwner after attach_ready returned false, want true")
	}
	if ok := reg.RouteTerminalBytesIfOwner("sess-1", owner, protocol.AttachPacket{
		Type:     protocol.AttachPacketTypeTerminalBytes,
		ClientID: "client-1",
		Payload:  []byte("snapshot"),
	}); !ok {
		t.Fatal("RouteTerminalBytesIfOwner returned false, want true")
	}
	anchors := []protocol.SubmitAnchor{
		{ID: "submit-1", Line: 2, SubmittedAt: 1775131200},
		{ID: "submit-2", Line: 5, SubmittedAt: 1775131300},
	}
	if ok := reg.RouteSnapshotDoneIfOwner("sess-1", owner, "client-1", anchors...); !ok {
		t.Fatal("RouteSnapshotDoneIfOwner returned false, want true")
	}

	controls := client.Controls()
	if len(controls) != 3 {
		t.Fatalf("control count = %d, want 3", len(controls))
	}
	if controls[0].Type != "attached" || controls[1].Type != "resize" || controls[2].Type != "snapshot_done" {
		t.Fatalf("controls = %#v, want attached then resize then snapshot_done", controls)
	}
	if !reflect.DeepEqual(controls[2].SubmitAnchors, anchors) {
		t.Fatalf("snapshot_done anchors = %#v, want %#v", controls[2].SubmitAnchors, anchors)
	}
	if controls[1].Cols != 121 || controls[1].Rows != 41 {
		t.Fatalf("resize = %#v, want cols=121 rows=41", controls[1])
	}
	if binaries := client.Binaries(); len(binaries) != 1 || string(binaries[0]) != "snapshot" {
		t.Fatalf("binaries = %#v, want one snapshot payload", binaries)
	}

	if detached := reg.DetachClient("sess-1", "client-1", "client_closed"); !detached {
		t.Fatal("DetachClient returned false, want true")
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "client_closed" {
		t.Fatalf("close reasons = %#v, want [client_closed]", reasons)
	}
	frames := owner.Frames()
	if len(frames) != 1 || frames[0].Type != "attach_close" || frames[0].ClientID != "client-1" {
		t.Fatalf("owner frames = %#v, want attach_close for client-1", frames)
	}
}

func TestRegistryRoutesSnapshotDoneSanitizesSubmitAnchors(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, owner)

	if _, err := reg.StartAttach("sess-1", "client-1", client); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}
	if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); !ok {
		t.Fatal("RouteAttachReadyIfOwner returned false, want true")
	}

	anchors := []protocol.SubmitAnchor{
		{ID: "", Line: 1, SubmittedAt: 1775131200},
		{ID: "negative-line", Line: -1, SubmittedAt: 1775131200},
		{ID: "negative-time", Line: 1, SubmittedAt: -1},
		{ID: "submit-1", Line: 2, SubmittedAt: 1775131200},
	}
	for i := 2; i <= protocol.MaxSubmitAnchors+2; i++ {
		anchors = append(anchors, protocol.SubmitAnchor{
			ID:          "submit-" + strconv.Itoa(i),
			Line:        i,
			SubmittedAt: 1775131200 + i,
		})
	}

	if ok := reg.RouteSnapshotDoneIfOwner("sess-1", owner, "client-1", anchors...); !ok {
		t.Fatal("RouteSnapshotDoneIfOwner returned false, want true")
	}

	controls := client.Controls()
	if len(controls) != 2 {
		t.Fatalf("control count = %d, want 2", len(controls))
	}
	got := controls[1].SubmitAnchors
	if len(got) != protocol.MaxSubmitAnchors {
		t.Fatalf("anchor count = %d, want %d", len(got), protocol.MaxSubmitAnchors)
	}
	if got[0].ID != "submit-1" {
		t.Fatalf("first anchor = %#v, want submit-1", got[0])
	}
	if got[len(got)-1].ID != "submit-"+strconv.Itoa(protocol.MaxSubmitAnchors) {
		t.Fatalf("last anchor = %#v, want submit-%d", got[len(got)-1], protocol.MaxSubmitAnchors)
	}
}

func TestRegistryStartAttachForUserRejectsCrossUserAccess(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	}, SessionOwner{UserID: 42, AgentTokenID: "agt-1"}, owner)

	if _, err := reg.StartAttachForUser("sess-1", "client-1", 7, client); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("StartAttachForUser error = %v, want ErrSessionNotFound", err)
	}
	if frames := owner.Frames(); len(frames) != 0 {
		t.Fatalf("owner frames = %#v, want none", frames)
	}
}

func TestRegistryPendingAttachDisconnectBeforeReadyPreventsLaterDelivery(t *testing.T) {
	reg := NewRegistry()
	owner := &recordingPeer{}
	client := &recordingAttachPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, owner)

	if _, err := reg.StartAttach("sess-1", "client-1", client); err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	if detached := reg.DetachClient("sess-1", "client-1", "client_closed"); !detached {
		t.Fatal("DetachClient returned false, want true")
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "client_closed" {
		t.Fatalf("close reasons = %#v, want [client_closed]", reasons)
	}

	frames := owner.Frames()
	if len(frames) != 1 || frames[0].Type != "attach_close" || frames[0].ClientID != "client-1" {
		t.Fatalf("owner frames = %#v, want attach_close for pending client-1", frames)
	}

	if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); ok {
		t.Fatal("RouteAttachReadyIfOwner returned true after pending detach, want false")
	}
	if ok := reg.RouteSnapshotDoneIfOwner("sess-1", owner, "client-1"); ok {
		t.Fatal("RouteSnapshotDoneIfOwner returned true after pending detach, want false")
	}
	if ok := reg.RouteTerminalBytesIfOwner("sess-1", owner, protocol.AttachPacket{
		Type:     protocol.AttachPacketTypeTerminalBytes,
		ClientID: "client-1",
		Payload:  []byte("snapshot"),
	}); ok {
		t.Fatal("RouteTerminalBytesIfOwner returned true after pending detach, want false")
	}
}

func TestRegistryDetachesSlowAttachClientsOnSendFailure(t *testing.T) {
	tests := []struct {
		name   string
		client *failingAttachPeer
		run    func(*testing.T, *Registry, AgentPeer) bool
	}{
		{
			name:   "attach_ready control failure",
			client: &failingAttachPeer{failControlAt: 1},
			run: func(t *testing.T, reg *Registry, owner AgentPeer) bool {
				return reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40)
			},
		},
		{
			name:   "resize control failure",
			client: &failingAttachPeer{failControlAt: 2},
			run: func(t *testing.T, reg *Registry, owner AgentPeer) bool {
				if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); !ok {
					t.Fatal("RouteAttachReadyIfOwner returned false, want true")
				}
				return reg.RouteResizeIfOwner("sess-1", owner, 121, 41)
			},
		},
		{
			name:   "snapshot_done control failure",
			client: &failingAttachPeer{failControlAt: 2},
			run: func(t *testing.T, reg *Registry, owner AgentPeer) bool {
				if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); !ok {
					t.Fatal("RouteAttachReadyIfOwner returned false, want true")
				}
				return reg.RouteSnapshotDoneIfOwner("sess-1", owner, "client-1")
			},
		},
		{
			name:   "terminal bytes failure",
			client: &failingAttachPeer{failBinaryAt: 1},
			run: func(t *testing.T, reg *Registry, owner AgentPeer) bool {
				if ok := reg.RouteAttachReadyIfOwner("sess-1", owner, "client-1", 120, 40); !ok {
					t.Fatal("RouteAttachReadyIfOwner returned false, want true")
				}
				return reg.RouteTerminalBytesIfOwner("sess-1", owner, protocol.AttachPacket{
					Type:     protocol.AttachPacketTypeTerminalBytes,
					ClientID: "client-1",
					Payload:  []byte("snapshot"),
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			owner := &recordingPeer{}
			reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, owner)

			if _, err := reg.StartAttach("sess-1", "client-1", tc.client); err != nil {
				t.Fatalf("StartAttach returned error: %v", err)
			}

			if ok := tc.run(t, reg, owner); !ok {
				t.Fatal("attach route returned false, want true")
			}

			if reasons := tc.client.CloseReasons(); len(reasons) != 1 || reasons[0] != "slow_client" {
				t.Fatalf("close reasons = %#v, want [slow_client]", reasons)
			}

			frames := owner.Frames()
			if len(frames) != 1 {
				t.Fatalf("owner frames = %#v, want one attach_close", frames)
			}
			if frames[0].Type != "attach_close" || frames[0].ClientID != "client-1" || frames[0].Reason != "slow_client" {
				t.Fatalf("owner frame = %#v, want attach_close slow_client for client-1", frames[0])
			}

			if detached := reg.DetachClient("sess-1", "client-1", "client_closed"); detached {
				t.Fatal("DetachClient returned true after slow_client cleanup, want false")
			}
		})
	}
}

func TestRegistryDisconnectUserSessionsRemovesOwnedSessionsAndClosesAttaches(t *testing.T) {
	reg := NewRegistry()
	ownerA := &recordingPeer{}
	ownerB := &recordingPeer{}
	clientA := &recordingAttachPeer{}
	clientB := &recordingAttachPeer{}

	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-a", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-a"}, ownerA)
	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-b", Launcher: "codex"}, SessionOwner{UserID: 2, AgentTokenID: "agt-b"}, ownerB)

	if _, err := reg.StartAttach("sess-a", "client-a", clientA); err != nil {
		t.Fatalf("StartAttach sess-a returned error: %v", err)
	}
	if _, err := reg.StartAttach("sess-b", "client-b", clientB); err != nil {
		t.Fatalf("StartAttach sess-b returned error: %v", err)
	}

	disconnected := reg.DisconnectUserSessions(1, "account_deleted")
	if disconnected != 1 {
		t.Fatalf("DisconnectUserSessions = %d, want 1", disconnected)
	}
	if _, ok := reg.Session("sess-a"); ok {
		t.Fatal("Session sess-a still exists after user disconnect")
	}
	if _, ok := reg.Session("sess-b"); !ok {
		t.Fatal("Session sess-b missing after unrelated user disconnect")
	}
	if reasons := clientA.CloseReasons(); len(reasons) != 1 || reasons[0] != "account_deleted" {
		t.Fatalf("clientA close reasons = %#v, want [account_deleted]", reasons)
	}
	if reasons := clientB.CloseReasons(); len(reasons) != 0 {
		t.Fatalf("clientB close reasons = %#v, want none", reasons)
	}
	if ownerA.closed != 1 {
		t.Fatalf("ownerA closed = %d, want 1", ownerA.closed)
	}
	if ownerB.closed != 0 {
		t.Fatalf("ownerB closed = %d, want 0", ownerB.closed)
	}
}

func TestRegistryDisconnectAgentTokenSessionsMatchesTokenOwnership(t *testing.T) {
	reg := NewRegistry()
	ownerA := &recordingPeer{}
	ownerB := &recordingPeer{}

	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-a", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-a"}, ownerA)
	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-b", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-b"}, ownerB)

	disconnected := reg.DisconnectAgentTokenSessions("agt-b", "agent_token_revoked")
	if disconnected != 1 {
		t.Fatalf("DisconnectAgentTokenSessions = %d, want 1", disconnected)
	}
	if _, ok := reg.Session("sess-a"); !ok {
		t.Fatal("sess-a missing after unrelated token disconnect")
	}
	if _, ok := reg.Session("sess-b"); ok {
		t.Fatal("sess-b still exists after token disconnect")
	}
	if ownerA.closed != 0 {
		t.Fatalf("ownerA closed = %d, want 0", ownerA.closed)
	}
	if ownerB.closed != 1 {
		t.Fatalf("ownerB closed = %d, want 1", ownerB.closed)
	}
}

func TestRegistrySnapshotJSONRoundTrip(t *testing.T) {
	info := protocol.SessionInfo{
		SessionID:      "sess-1",
		DeviceID:       "dev-1",
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      10,
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded protocol.SessionInfo
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", decoded.SessionID)
	}
	if decoded.DeviceID != "dev-1" {
		t.Fatalf("DeviceID = %q, want dev-1", decoded.DeviceID)
	}
	if decoded.StartedAt != 10 {
		t.Fatalf("StartedAt = %d, want 10", decoded.StartedAt)
	}
}

func parseRegistryLogEntries(t *testing.T, raw []byte) []map[string]any {
	t.Helper()

	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("Unmarshal returned error: %v\nline: %s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
