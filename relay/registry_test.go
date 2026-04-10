package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"yuanbohan/tunnel/protocol"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendJSON(any) error      { return nil }
func (fakeAgentPeer) SendBinary([]byte) error { return nil }
func (fakeAgentPeer) Close() error            { return nil }

type recordingPeer struct {
	mu       sync.Mutex
	frames   []protocol.AgentFrame
	binaries [][]byte
	closed   int
}

type blockingPeer struct {
	started sync.Once
	ready   chan struct{}
	release chan struct{}
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

func (p *recordingPeer) SendBinary(payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binaries = append(p.binaries, append([]byte(nil), payload...))
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

func (p *blockingPeer) SendBinary([]byte) error { return nil }

func (p *blockingPeer) Close() error { return nil }

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

func TestRegistryRegisterAndListSortedByLastActive(t *testing.T) {
	reg := NewRegistry()
	olderActive := 20
	newerActive := 30
	older := protocol.SessionInfo{
		SessionID:    "a",
		Launcher:     "codex",
		StartedAt:    10,
		LastActiveAt: &olderActive,
	}
	newer := protocol.SessionInfo{
		SessionID:    "b",
		Launcher:     "gemini",
		StartedAt:    11,
		LastActiveAt: &newerActive,
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
	if got[0].State != protocol.SessionStateConnected || got[1].State != protocol.SessionStateConnected {
		t.Fatalf("states = %#v, want connected", got)
	}
}

func TestRegistryMissingSessionErrors(t *testing.T) {
	reg := NewRegistry()

	if err := reg.WriteInput("missing", protocol.EncodeInputText("x", false)); err != ErrSessionNotFound {
		t.Fatalf("WriteInput error = %v, want ErrSessionNotFound", err)
	}
}

func TestRegistryTouchActivityUpdatesLastActive(t *testing.T) {
	reg := NewRegistry()
	info := protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: 10}
	peer := fakeAgentPeer{}
	reg.Register(info, peer)

	now := 40
	if ok := reg.TouchActivityIfOwner("sess-1", peer, now); !ok {
		t.Fatal("TouchActivityIfOwner returned false, want true")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LastActiveAt == nil || *got[0].LastActiveAt != now {
		t.Fatalf("LastActiveAt = %v, want %v", got[0].LastActiveAt, now)
	}
	if got[0].State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", got[0].State)
	}
}

func TestRegistryReplaceSessionIDLogsSessionReplaced(t *testing.T) {
	reg := NewRegistry()
	logs := &bytes.Buffer{}
	reg.SetLogger(NewLogger(logs))

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

func TestRegistryDisconnectMarksSessionReconnectingAndClosesAttaches(t *testing.T) {
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

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false after disconnect, want true")
	}
	if info.State != protocol.SessionStateReconnecting {
		t.Fatalf("State = %q, want reconnecting", info.State)
	}
	if reasons := attachPeer.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_reconnecting" {
		t.Fatalf("close reasons = %#v, want [session_reconnecting]", reasons)
	}
}

func TestRegistryReconnectWithinGraceRestoresConnectedAndKeepsNewestMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 30 * time.Millisecond
	oldPeer := &recordingPeer{}
	oldActive := 40
	reg.Register(protocol.SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "codex",
		StartedAt:    10,
		LastActiveAt: &oldActive,
	}, oldPeer)

	if disconnected := reg.DisconnectIfOwner("sess-1", oldPeer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	newPeer := &recordingPeer{}
	newActive := 50
	reg.Register(protocol.SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "codex",
		StartedAt:    10,
		LastActiveAt: &newActive,
	}, newPeer)

	time.Sleep(2 * reg.reconnectGrace)

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false after reconnect, want true")
	}
	if info.State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", info.State)
	}
	if info.LastActiveAt == nil || *info.LastActiveAt != newActive {
		t.Fatalf("LastActiveAt = %v, want %v", info.LastActiveAt, newActive)
	}
}

func TestRegistryDisconnectGraceExpiryRemovesSessionOnce(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 20 * time.Millisecond
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	waitForCondition(t, time.Second, func() bool {
		_, ok := reg.Session("sess-1")
		return !ok
	})
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
	if info.State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", info.State)
	}
	if err := reg.WriteInput("sess-1", protocol.EncodeInputText("ping", false)); err != nil {
		t.Fatalf("WriteInput returned error after stale-owner disconnect: %v", err)
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
	oldPeer := &wsAgentPeer{conn: &mockWSConn{}, active: true}
	client := &recordingAttachPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)

	startedOwner, err := reg.StartAttach("sess-1", "client-1", client)
	if err != nil {
		t.Fatalf("StartAttach returned error: %v", err)
	}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})

	if err := startedOwner.SendJSON(protocol.AttachOpenFrame("client-1")); !errors.Is(err, errAgentPeerInactive) {
		t.Fatalf("stale owner SendJSON error = %v, want errAgentPeerInactive", err)
	}
	if reasons := client.CloseReasons(); len(reasons) != 1 || reasons[0] != "session_reconnecting" {
		t.Fatalf("close reasons = %#v, want [session_reconnecting]", reasons)
	}
}

func TestRegistryWriteInputReturnsReconnectingForDisconnectedSession(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	reg.DisconnectIfOwner("sess-1", peer)

	err := reg.WriteInput("sess-1", protocol.EncodeInputText("ping", false))
	if !errors.Is(err, ErrSessionReconnecting) {
		t.Fatalf("WriteInput error = %v, want ErrSessionReconnecting", err)
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
		sendDone <- reg.WriteInput("sess-1", protocol.EncodeInputText("ping", false))
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
			t.Fatalf("WriteInput returned error: %v", err)
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
	if ok := reg.RouteSnapshotDoneIfOwner("sess-1", owner, "client-1"); !ok {
		t.Fatal("RouteSnapshotDoneIfOwner returned false, want true")
	}

	controls := client.Controls()
	if len(controls) != 3 {
		t.Fatalf("control count = %d, want 3", len(controls))
	}
	if controls[0].Type != "attached" || controls[1].Type != "resize" || controls[2].Type != "snapshot_done" {
		t.Fatalf("controls = %#v, want attached then resize then snapshot_done", controls)
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

func TestRegistrySnapshotJSONRoundTrip(t *testing.T) {
	info := protocol.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      10,
		State:          protocol.SessionStateConnected,
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
	if decoded.State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", decoded.State)
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
