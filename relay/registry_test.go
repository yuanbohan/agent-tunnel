package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
	updates []protocol.ClientUpdateMessage
}

func (s *recordingClientUpdateSink) WriteClientUpdate(msg protocol.ClientUpdateMessage) error {
	s.updates = append(s.updates, msg)
	return nil
}

func (s *recordingClientUpdateSink) Close() error { return nil }

func TestRegistryRegisterAndListSortedByLastActive(t *testing.T) {
	reg := NewRegistry()
	olderActive := time.Unix(20, 0)
	newerActive := time.Unix(30, 0)
	older := protocol.SessionInfo{SessionID: "a", Launcher: "codex", StartedAt: time.Unix(10, 0), LastActiveAt: &olderActive}
	newer := protocol.SessionInfo{SessionID: "b", Launcher: "gemini", StartedAt: time.Unix(11, 0), LastActiveAt: &newerActive}

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

	now := time.Unix(40, 0)
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("focused test\n"), 120, 40, now); !ok {
		t.Fatal("TouchOutputIfOwner returned false, want true")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LatestSeq != 1 {
		t.Fatalf("LatestSeq = %d, want 1", got[0].LatestSeq)
	}
	if got[0].LastActiveAt == nil || !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want %v", got[0].LastActiveAt, now)
	}
}

func TestRegistryTouchOutputBroadcastsGlobalClientUpdate(t *testing.T) {
	reg := NewRegistry()
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)
	peer := fakeAgentPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	now := time.Unix(40, 0).UTC()
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("hello"), 132, 43, now); !ok {
		t.Fatal("TouchOutputIfOwner returned false, want true")
	}

	if len(updateSink.updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updateSink.updates))
	}
	got := updateSink.updates[0]
	if got.SessionID != "sess-1" || got.Type != "output" || got.Seq != 1 {
		t.Fatalf("update = %#v, want sess-1 output seq 1", got)
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

func TestRegistryFramesFilterByInclusiveRange(t *testing.T) {
	reg := NewRegistry()
	peer := fakeAgentPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if !reg.TouchOutputIfOwner("sess-1", peer, []byte("one"), 120, 40, time.Unix(40, 0).UTC()) {
		t.Fatal("TouchOutputIfOwner returned false for frame 1")
	}
	if !reg.TouchOutputIfOwner("sess-1", peer, []byte("two"), 120, 40, time.Unix(41, 0).UTC()) {
		t.Fatal("TouchOutputIfOwner returned false for frame 2")
	}
	frameTime := time.Unix(42, 0).UTC()
	if !reg.TouchOutputIfOwner("sess-1", peer, []byte("three"), 132, 43, frameTime) {
		t.Fatal("TouchOutputIfOwner returned false for frame 3")
	}

	frames, ok := reg.Frames("sess-1", 2, true, 2, true)
	if !ok {
		t.Fatal("Frames returned false, want true")
	}
	if len(frames) != 1 {
		t.Fatalf("len(Frames) = %d, want 1", len(frames))
	}
	if frames[0].Seq != 2 {
		t.Fatalf("Seq = %d, want 2", frames[0].Seq)
	}
	if frames[0].Cols != 120 || frames[0].Rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", frames[0].Cols, frames[0].Rows)
	}
	if frames[0].TS.IsZero() {
		t.Fatal("expected non-zero timestamp")
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

func TestRegistryRemoveIfOwnerBroadcastsRemovalOnly(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if removed := reg.RemoveIfOwner("sess-1", peer); !removed {
		t.Fatal("RemoveIfOwner returned false, want true")
	}
	if len(updateSink.updates) != 1 {
		t.Fatalf("client update count = %d, want 1", len(updateSink.updates))
	}
	if updateSink.updates[0].Type != "session_removed" {
		t.Fatalf("client updates = %#v, want removal only", updateSink.updates)
	}
}

func TestRegistryRemoveIfOwnerSkipsStaleOwnerAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

	if removed := reg.RemoveIfOwner("sess-1", oldPeer); removed {
		t.Fatal("RemoveIfOwner returned true for stale owner, want false")
	}

	if err := reg.WriteInput("sess-1", protocol.EncodeInputText("ping", false)); err != nil {
		t.Fatalf("WriteInput returned error after stale-owner cleanup: %v", err)
	}
	messages := newPeer.Messages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0]; got.Type != "input_text" || got.Text != "ping" || got.Submit {
		t.Fatalf("new peer input = %#v, want input_text ping submit=false", got)
	}
}

func TestRegistrySnapshotJSONRoundTrip(t *testing.T) {
	info := protocol.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      time.Unix(10, 0),
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
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}
