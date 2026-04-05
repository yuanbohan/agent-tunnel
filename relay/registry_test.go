package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"yuanbohan/tunnel/protocol"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendInput([]byte) error { return nil }
func (fakeAgentPeer) Close() error           { return nil }

type recordingPeer struct {
	inputs [][]byte
	closed int
}

func (p *recordingPeer) SendInput(data []byte) error {
	p.inputs = append(p.inputs, append([]byte(nil), data...))
	return nil
}

func (p *recordingPeer) Close() error {
	p.closed++
	return nil
}

type recordingClientUpdateSink struct {
	updates []protocol.ClientUpdateMessage
}

func (s *recordingClientUpdateSink) WriteClientUpdate(msg protocol.ClientUpdateMessage) error {
	s.updates = append(s.updates, msg)
	return nil
}

func (s *recordingClientUpdateSink) Close() error { return nil }

type blockingPeer struct {
	sendStarted chan struct{}
	sendRelease chan struct{}
	inputs      [][]byte
}

func (p *blockingPeer) SendInput(data []byte) error {
	p.inputs = append(p.inputs, append([]byte(nil), data...))
	close(p.sendStarted)
	<-p.sendRelease
	return nil
}

func (p *blockingPeer) Close() error { return nil }

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

	if err := reg.WriteInput("missing", []byte("x")); err != ErrSessionNotFound {
		t.Fatalf("WriteInput error = %v, want ErrSessionNotFound", err)
	}
}

func TestRegistryTouchOutputUpdatesSeqAndLastActive(t *testing.T) {
	reg := NewRegistry()
	info := protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: time.Unix(10, 0)}
	reg.Register(info, fakeAgentPeer{})

	now := time.Unix(40, 0)
	reg.TouchOutput("sess-1", []byte("\x1b[32mPASS\x1b[0m focused test\n"), now)

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LatestSeq != 1 {
		t.Fatalf("LatestSeq = %d, want 1", got[0].LatestSeq)
	}
	if got[0].LastActiveAt == nil {
		t.Fatal("LastActiveAt = nil, want 40")
	}
	if !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want 40", got[0].LastActiveAt)
	}
}

func TestRegistryTouchOutputIfOwnerUpdatesSeqForCurrentOwner(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	now := time.Unix(45, 0)
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("\x1b[32mPASS\x1b[0m current owner\n"), now); !ok {
		t.Fatal("TouchOutputIfOwner returned false for current owner")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LatestSeq != 1 {
		t.Fatalf("LatestSeq = %d, want 1", got[0].LatestSeq)
	}
	if got[0].LastActiveAt == nil || !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want 45", got[0].LastActiveAt)
	}
}

func TestRegistryReplaceSessionIDPreservesHistoryAndClosesOldPeer(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)
	reg.TouchOutput("sess-1", []byte("\x1b[32mPASS\x1b[0m focused test\n"), time.Unix(40, 0))

	if got := reg.List(); len(got) != 1 || got[0].LatestSeq != 1 {
		t.Fatalf("registered session = %#v, want latest_seq 1", got)
	}
	if oldPeer.closed != 1 {
		t.Fatalf("old peer close count = %d, want 1", oldPeer.closed)
	}
}

func TestRegistryTouchOutputBroadcastsGlobalClientUpdate(t *testing.T) {
	reg := NewRegistry()
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	reg.TouchOutput("sess-1", []byte("hello"), time.Unix(40, 0))

	if len(updateSink.updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updateSink.updates))
	}
	got := updateSink.updates[0]
	if got.SessionID != "sess-1" || got.Type != "output" || got.Seq != 1 {
		t.Fatalf("update = %#v, want sess-1 output seq 1", got)
	}
	data, err := base64.StdEncoding.DecodeString(got.Data)
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
	if got := entries[0]["level"]; got != "WARN" {
		t.Fatalf("level = %v, want WARN", got)
	}
	if got := entries[0]["session_id"]; got != "sess-1" {
		t.Fatalf("session_id = %v, want sess-1", got)
	}
}

func TestRegistryUpdateSessionStateIfOwnerUpdatesSnapshotAndBroadcasts(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	since := time.Unix(100, 0).UTC()
	changedAt := time.Unix(101, 0).UTC()
	if ok := reg.UpdateSessionStateIfOwner("sess-1", peer, protocol.SessionStateActionRequired, changedAt, &since); !ok {
		t.Fatal("UpdateSessionStateIfOwner returned false, want true")
	}

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false, want true")
	}
	if info.State != protocol.SessionStateActionRequired {
		t.Fatalf("State = %q, want %q", info.State, protocol.SessionStateActionRequired)
	}
	if info.StateChangedAt == nil || !info.StateChangedAt.Equal(changedAt) {
		t.Fatalf("StateChangedAt = %v, want %v", info.StateChangedAt, changedAt)
	}
	if info.ActionRequiredSince == nil || !info.ActionRequiredSince.Equal(since) {
		t.Fatalf("ActionRequiredSince = %v, want %v", info.ActionRequiredSince, since)
	}
	if len(updateSink.updates) != 1 {
		t.Fatalf("client update count = %d, want 1", len(updateSink.updates))
	}
	if updateSink.updates[0].Type != "session_state" || updateSink.updates[0].State != protocol.SessionStateActionRequired {
		t.Fatalf("client update = %#v, want session_state action_required", updateSink.updates[0])
	}
}

func TestRegistryRemoveIfOwnerBroadcastsResolvedNormalEvent(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	updateSink := &recordingClientUpdateSink{}
	reg.AddUpdateSink("updates-1", updateSink)

	reg.Register(protocol.SessionInfo{
		SessionID:           "sess-1",
		Launcher:            "codex",
		State:               protocol.SessionStateActionRequired,
		ActionRequiredSince: cloneTimePtr(ptrTime(time.Unix(100, 0).UTC())),
	}, peer)

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

func ptrTime(value time.Time) *time.Time {
	return &value
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

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if err := reg.WriteInput("sess-1", []byte("ping")); err != nil {
		t.Fatalf("WriteInput returned error after stale-owner cleanup: %v", err)
	}
	if got := string(bytes.Join(newPeer.inputs, nil)); got != "ping" {
		t.Fatalf("new peer input = %q, want ping", got)
	}
	if len(oldPeer.inputs) != 0 {
		t.Fatalf("old peer received input = %#v, want none", oldPeer.inputs)
	}
}

func TestRegistryTouchOutputIfOwnerSkipsStaleOwnerAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

	now := time.Unix(50, 0)
	if ok := reg.TouchOutputIfOwner("sess-1", oldPeer, []byte("\x1b[32mPASS\x1b[0m stale output\n"), now); ok {
		t.Fatal("TouchOutputIfOwner returned true for stale owner, want false")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LastActiveAt != nil {
		t.Fatalf("LastActiveAt = %v, want nil", got[0].LastActiveAt)
	}
	if len(newPeer.inputs) != 0 {
		t.Fatalf("new peer received input-like output = %#v, want none", newPeer.inputs)
	}
}

func TestRegistryTouchOutputIfOwnerIgnoresStaleOldOwnerOutputAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

	if ok := reg.TouchOutputIfOwner("sess-1", oldPeer, []byte("stale"), time.Unix(60, 0)); ok {
		t.Fatal("TouchOutputIfOwner returned true for stale old owner, want false")
	}

	info := reg.List()
	if len(info) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(info))
	}
	if info[0].LatestSeq != 0 {
		t.Fatalf("LatestSeq = %d, want 0", info[0].LatestSeq)
	}
}

func TestRegistryMarkReadClampsToLatestSeq(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	reg.TouchOutput("sess-1", []byte("one"), time.Unix(10, 0))
	reg.TouchOutput("sess-1", []byte("two"), time.Unix(11, 0))

	info, ok := reg.MarkRead("sess-1", 5)
	if !ok {
		t.Fatal("MarkRead returned false for live session")
	}
	if info.LastReadSeq != 2 {
		t.Fatalf("LastReadSeq = %d, want 2", info.LastReadSeq)
	}
	if info.UnreadCount != 0 {
		t.Fatalf("UnreadCount = %d, want 0", info.UnreadCount)
	}
}

func TestRegistryRetainsFrameAtExactHistoryBudgetBoundary(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	first := bytes.Repeat([]byte("a"), maxSessionHistoryBytes/2)
	second := bytes.Repeat([]byte("b"), maxSessionHistoryBytes/2)

	reg.TouchOutput("sess-1", first, time.Unix(10, 0))
	reg.TouchOutput("sess-1", second, time.Unix(11, 0))

	page, ok := reg.History("sess-1", 0)
	if !ok {
		t.Fatal("History returned false for live session")
	}
	if len(page.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2 at exact budget boundary", len(page.Messages))
	}
	if page.Messages[0].Seq != 1 || page.Messages[1].Seq != 2 {
		t.Fatalf("seqs = [%d %d], want [1 2]", page.Messages[0].Seq, page.Messages[1].Seq)
	}
}

func TestRegistryRetainsOnlyWholeFramesWithinHistoryBudget(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	half := maxSessionHistoryBytes/2 + 1
	frame1 := bytes.Repeat([]byte("a"), half)
	frame2 := bytes.Repeat([]byte("b"), half)
	frame3 := []byte("c")

	reg.TouchOutput("sess-1", frame1, time.Unix(10, 0))
	reg.TouchOutput("sess-1", frame2, time.Unix(11, 0))
	reg.TouchOutput("sess-1", frame3, time.Unix(12, 0))

	page, ok := reg.History("sess-1", 0)
	if !ok {
		t.Fatal("History returned false for live session")
	}
	if len(page.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(page.Messages))
	}
	if page.Messages[0].Seq != 2 || page.Messages[1].Seq != 3 {
		t.Fatalf("seqs = [%d %d], want [2 3]", page.Messages[0].Seq, page.Messages[1].Seq)
	}
	if len(page.Messages[0].DataB64) == 0 || len(page.Messages[1].DataB64) == 0 {
		t.Fatal("expected retained messages to keep full frame payloads")
	}
}

func TestRegistryHistorySupportsAfterSync(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	for _, output := range []string{"one", "two", "three", "four"} {
		reg.TouchOutput("sess-1", []byte(output), time.Unix(10, 0))
	}

	page, ok := reg.History("sess-1", 2)
	if !ok {
		t.Fatal("History returned false for live session")
	}
	if len(page.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(page.Messages))
	}
	if page.Messages[0].Seq != 3 || page.Messages[1].Seq != 4 {
		t.Fatalf("seqs = [%d %d], want [3 4]", page.Messages[0].Seq, page.Messages[1].Seq)
	}
}

func TestRegistryWriteInputWaitsForInFlightOldPeerBeforeReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &blockingPeer{
		sendStarted: make(chan struct{}),
		sendRelease: make(chan struct{}),
	}
	newPeer := &recordingPeer{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- reg.WriteInput("sess-1", []byte("ping"))
	}()

	select {
	case <-oldPeer.sendStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for old peer SendInput to start")
	}

	registerDone := make(chan struct{})
	go func() {
		reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)
		close(registerDone)
	}()

	select {
	case <-registerDone:
		t.Fatal("Register completed before in-flight input finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(oldPeer.sendRelease)

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteInput returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for WriteInput to finish")
	}

	select {
	case <-registerDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Register to finish")
	}

	if err := reg.WriteInput("sess-1", []byte("pong")); err != nil {
		t.Fatalf("WriteInput after replacement returned error: %v", err)
	}
	if got := string(bytes.Join(oldPeer.inputs, nil)); got != "ping" {
		t.Fatalf("old peer input = %q, want ping", got)
	}
	if got := string(bytes.Join(newPeer.inputs, nil)); got != "pong" {
		t.Fatalf("new peer input = %q, want pong", got)
	}
}

func parseRegistryLogEntries(t *testing.T, raw []byte) []map[string]any {
	t.Helper()

	lines := bytes.Split(raw, []byte{'\n'})
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("unmarshal log line %q: %v", string(line), err)
		}
		entries = append(entries, entry)
	}
	return entries
}
