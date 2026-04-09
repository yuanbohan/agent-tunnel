package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"yuanbohan/tunnel/protocol"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) Send(protocol.Message) error { return nil }
func (fakeAgentPeer) Close() error                { return nil }

type recordingPeer struct {
	mu       sync.Mutex
	messages []protocol.Message
	closed   int
}

func (p *recordingPeer) Send(msg protocol.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, msg)
	return nil
}

func (p *recordingPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed++
	return nil
}

func (p *recordingPeer) Messages() []protocol.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.Message(nil), p.messages...)
}

type recordingClientUpdateSink struct {
	mu      sync.Mutex
	updates []protocol.ClientUpdateMessage
}

func (s *recordingClientUpdateSink) WriteClientUpdate(msg protocol.ClientUpdateMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, msg)
	return nil
}

func (s *recordingClientUpdateSink) Close() error { return nil }

func (s *recordingClientUpdateSink) Updates() []protocol.ClientUpdateMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.ClientUpdateMessage(nil), s.updates...)
}

func TestRegistryRegisterAndListSortedByLastActive(t *testing.T) {
	reg := NewRegistry()
	olderActive := time.Unix(20, 0)
	newerActive := time.Unix(30, 0)
	older := protocol.SessionInfo{
		SessionID:    "a",
		Launcher:     "codex",
		StartedAt:    time.Unix(10, 0),
		LastActiveAt: &olderActive,
	}
	newer := protocol.SessionInfo{
		SessionID:    "b",
		Launcher:     "gemini",
		StartedAt:    time.Unix(11, 0),
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

func TestRegistryTouchOutputUpdatesSeqAndLastActive(t *testing.T) {
	reg := NewRegistry()
	info := protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: time.Unix(10, 0)}
	peer := fakeAgentPeer{}
	reg.Register(info, peer)

	now := time.Unix(40, 0).UTC()
	if ok := reg.TouchOutputIfOwner("sess-1", peer, replayFrame(3, "focused test\n", 120, 40, now)); !ok {
		t.Fatal("TouchOutputIfOwner returned false, want true")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LatestSeq != 3 {
		t.Fatalf("LatestSeq = %d, want 3", got[0].LatestSeq)
	}
	if got[0].LastActiveAt == nil || !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want %v", got[0].LastActiveAt, now)
	}
	if got[0].State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", got[0].State)
	}
}

func TestRegistryTouchOutputBroadcastsGlobalClientUpdate(t *testing.T) {
	reg := NewRegistry()
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)
	peer := fakeAgentPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	now := time.Unix(40, 0).UTC()
	if ok := reg.TouchOutputIfOwner("sess-1", peer, replayFrame(7, "hello", 132, 43, now)); !ok {
		t.Fatal("TouchOutputIfOwner returned false, want true")
	}

	updates := updateSink.Updates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.SessionID != "sess-1" || got.Type != "output" || got.Seq != 7 {
		t.Fatalf("update = %#v, want sess-1 output seq 7", got)
	}
	if got.TS == nil || !got.TS.Equal(now) {
		t.Fatalf("ts = %v, want %v", got.TS, now)
	}
	if got.Cols != 132 || got.Rows != 43 {
		t.Fatalf("size = %dx%d, want 132x43", got.Cols, got.Rows)
	}
	data, err := base64.StdEncoding.DecodeString(got.DataB64)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", string(data))
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

func TestRegistryDisconnectMarksSessionReconnectingWithoutRemoval(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

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
	if updates := updateSink.Updates(); len(updates) != 0 {
		t.Fatalf("updates = %#v, want no immediate session_removed broadcast", updates)
	}
}

func TestRegistryReconnectWithinGraceRestoresConnectedAndKeepsNewestMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 30 * time.Millisecond
	oldPeer := &recordingPeer{}
	oldActive := time.Unix(40, 0).UTC()
	reg.Register(protocol.SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "codex",
		StartedAt:    time.Unix(10, 0),
		LastActiveAt: &oldActive,
		LatestSeq:    3,
	}, oldPeer)

	if disconnected := reg.DisconnectIfOwner("sess-1", oldPeer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	newPeer := &recordingPeer{}
	newActive := time.Unix(50, 0).UTC()
	reg.Register(protocol.SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "codex",
		StartedAt:    time.Unix(10, 0),
		LastActiveAt: &newActive,
		LatestSeq:    5,
	}, newPeer)

	time.Sleep(2 * reg.reconnectGrace)

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false after reconnect, want true")
	}
	if info.State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", info.State)
	}
	if info.LatestSeq != 5 {
		t.Fatalf("LatestSeq = %d, want 5", info.LatestSeq)
	}
	if info.LastActiveAt == nil || !info.LastActiveAt.Equal(newActive) {
		t.Fatalf("LastActiveAt = %v, want %v", info.LastActiveAt, newActive)
	}
}

func TestRegistryDisconnectGraceExpiryRemovesSessionOnce(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 20 * time.Millisecond
	peer := &recordingPeer{}
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	waitForCondition(t, time.Second, func() bool {
		_, ok := reg.Session("sess-1")
		return !ok
	})

	updates := updateSink.Updates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Type != "session_removed" || updates[0].SessionID != "sess-1" {
		t.Fatalf("updates = %#v, want one session_removed for sess-1", updates)
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
	if info.State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", info.State)
	}
	if err := reg.WriteInput("sess-1", protocol.EncodeInputText("ping", false)); err != nil {
		t.Fatalf("WriteInput returned error after stale-owner disconnect: %v", err)
	}
	messages := newPeer.Messages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0]; got.Type != "input_text" || got.Text != "ping" || got.Submit {
		t.Fatalf("new peer input = %#v, want input_text ping submit=false", got)
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
	if messages := peer.Messages(); len(messages) != 0 {
		t.Fatalf("messages = %#v, want none", messages)
	}
}

func TestRegistryPendingHistoryFailsPromptlyOnDisconnect(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	_, resultCh, err := reg.StartHistoryRequest("sess-1", "history-1")
	if err != nil {
		t.Fatalf("StartHistoryRequest returned error: %v", err)
	}

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, ErrSessionReconnecting) {
			t.Fatalf("result.err = %v, want ErrSessionReconnecting", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending history request to fail")
	}
}

func TestRegistrySnapshotJSONRoundTrip(t *testing.T) {
	info := protocol.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      time.Unix(10, 0),
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

func replayFrame(seq uint64, data string, cols, rows int, ts time.Time) protocol.ReplayFrame {
	return protocol.EncodeReplayFrame(seq, []byte(data), cols, rows, ts)
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
