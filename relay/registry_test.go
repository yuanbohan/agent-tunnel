package relay

import (
	"bytes"
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

type recordingSink struct {
	chunks  [][]byte
	resizes [][2]int
	closed  int
	reasons []string
}

type sequencingSink struct {
	messages []protocol.Message
}

func (s *recordingSink) WriteOutput(data []byte) error {
	s.chunks = append(s.chunks, append([]byte(nil), data...))
	return nil
}

func (s *recordingSink) WriteResize(cols, rows int) error {
	s.resizes = append(s.resizes, [2]int{cols, rows})
	return nil
}

func (s *recordingSink) Close() error {
	s.closed++
	return nil
}

func (s *recordingSink) CloseWithReason(reason string) error {
	if reason != "" {
		s.reasons = append(s.reasons, reason)
	}
	return s.Close()
}

func (s *recordingSink) DisconnectReason() string {
	if len(s.reasons) == 0 {
		return ""
	}
	return s.reasons[len(s.reasons)-1]
}

func (s *sequencingSink) WriteOutput(data []byte) error {
	s.messages = append(s.messages, protocol.EncodeOutput(data))
	return nil
}

func (s *sequencingSink) WriteOutputFrame(seq uint64, data []byte, cols, rows int) error {
	s.messages = append(s.messages, protocol.EncodeOutputWithSeqAndSize(seq, data, cols, rows))
	return nil
}

func (s *sequencingSink) PreloadMessages(msgs []protocol.Message) error {
	s.messages = append(s.messages, msgs...)
	return nil
}

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

	if err := reg.AddSink("missing", "sink", &recordingSink{}); err != ErrSessionNotFound {
		t.Fatalf("AddSink error = %v, want ErrSessionNotFound", err)
	}
	if err := reg.WriteInput("missing", []byte("x")); err != ErrSessionNotFound {
		t.Fatalf("WriteInput error = %v, want ErrSessionNotFound", err)
	}
}

func TestRegistryTouchOutputUpdatesPreviewAndLastActive(t *testing.T) {
	reg := NewRegistry()
	info := protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: time.Unix(10, 0)}
	reg.Register(info, fakeAgentPeer{})

	now := time.Unix(40, 0)
	reg.TouchOutput("sess-1", []byte("\x1b[32mPASS\x1b[0m focused test\n"), now)

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LastPreview != "PASS focused test" {
		t.Fatalf("LastPreview = %q, want PASS focused test", got[0].LastPreview)
	}
	if got[0].LastActiveAt == nil {
		t.Fatal("LastActiveAt = nil, want 40")
	}
	if !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want 40", got[0].LastActiveAt)
	}
}

func TestRegistryTouchOutputIfOwnerUpdatesPreviewAndFanoutForCurrentOwner(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}

	now := time.Unix(45, 0)
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("\x1b[32mPASS\x1b[0m current owner\n"), now); !ok {
		t.Fatal("TouchOutputIfOwner returned false for current owner")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LastPreview != "PASS current owner" {
		t.Fatalf("LastPreview = %q, want PASS current owner", got[0].LastPreview)
	}
	if got[0].LastActiveAt == nil || !got[0].LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want 45", got[0].LastActiveAt)
	}
	if got := string(bytes.Join(sink.chunks, nil)); got != "\x1b[32mPASS\x1b[0m current owner\n" {
		t.Fatalf("sink output = %q, want raw chunk", got)
	}
}

func TestRegistryReplaceSessionIDPreservesSinksAndFansOutToThem(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)
	reg.TouchOutput("sess-1", []byte("\x1b[32mPASS\x1b[0m focused test\n"), time.Unix(40, 0))

	if got := string(bytes.Join(sink.chunks, nil)); got != "\x1b[32mPASS\x1b[0m focused test\n" {
		t.Fatalf("sink output = %q, want raw chunk", got)
	}
	if got := reg.List(); len(got) != 1 || got[0].LastPreview != "PASS focused test" {
		t.Fatalf("registered session = %#v, want preview PASS focused test", got)
	}
	if oldPeer.closed != 1 {
		t.Fatalf("old peer close count = %d, want 1", oldPeer.closed)
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

func TestRegistryRemoveIfOwnerClosesAttachedSinks(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}

	if removed := reg.RemoveIfOwner("sess-1", peer); !removed {
		t.Fatal("RemoveIfOwner returned false, want true")
	}
	if sink.closed != 1 {
		t.Fatalf("sink closed count = %d, want 1", sink.closed)
	}
	if got := sink.DisconnectReason(); got != "agent_disconnected" {
		t.Fatalf("sink disconnect reason = %q, want agent_disconnected", got)
	}
}

func TestRegistryTouchOutputIfOwnerSkipsStaleOwnerAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

	now := time.Unix(50, 0)
	if ok := reg.TouchOutputIfOwner("sess-1", oldPeer, []byte("\x1b[32mPASS\x1b[0m stale output\n"), now); ok {
		t.Fatal("TouchOutputIfOwner returned true for stale owner, want false")
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].LastPreview != "" {
		t.Fatalf("LastPreview = %q, want empty", got[0].LastPreview)
	}
	if got[0].LastActiveAt != nil {
		t.Fatalf("LastActiveAt = %v, want nil", got[0].LastActiveAt)
	}
	if len(sink.chunks) != 0 {
		t.Fatalf("sink received output = %#v, want none", sink.chunks)
	}
	if len(newPeer.inputs) != 0 {
		t.Fatalf("new peer received input-like output = %#v, want none", newPeer.inputs)
	}
}

func TestRegistryTouchOutputIfOwnerIgnoresStaleOldOwnerOutputAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}
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
	if len(sink.chunks) != 0 {
		t.Fatalf("sink received output = %#v, want none", sink.chunks)
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

func TestRegistryAttachSinkPreloadsBacklogBeforeLiveOutput(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	sink := &sequencingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	if ok := reg.UpdateSizeIfOwner("sess-1", peer, 120, 40); !ok {
		t.Fatal("UpdateSizeIfOwner returned false for current owner")
	}
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("one"), time.Unix(10, 0)); !ok {
		t.Fatal("TouchOutputIfOwner returned false for output one")
	}
	if ok := reg.UpdateSizeIfOwner("sess-1", peer, 132, 43); !ok {
		t.Fatal("UpdateSizeIfOwner returned false for current owner resize")
	}
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("two"), time.Unix(11, 0)); !ok {
		t.Fatal("TouchOutputIfOwner returned false for output two")
	}

	if err := reg.AttachSink("sess-1", "browser", sink, 1); err != nil {
		t.Fatalf("AttachSink returned error: %v", err)
	}
	if ok := reg.TouchOutputIfOwner("sess-1", peer, []byte("three"), time.Unix(12, 0)); !ok {
		t.Fatal("TouchOutputIfOwner returned false for output three")
	}

	if len(sink.messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(sink.messages))
	}
	if sink.messages[0].Seq != 2 || sink.messages[1].Seq != 3 {
		t.Fatalf("seqs = [%d %d], want [2 3]", sink.messages[0].Seq, sink.messages[1].Seq)
	}
	if sink.messages[0].Cols != 132 || sink.messages[0].Rows != 43 {
		t.Fatalf("backlog size = %dx%d, want 132x43", sink.messages[0].Cols, sink.messages[0].Rows)
	}
	if sink.messages[1].Cols != 132 || sink.messages[1].Rows != 43 {
		t.Fatalf("live size = %dx%d, want 132x43", sink.messages[1].Cols, sink.messages[1].Rows)
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

func TestRegistryBroadcastResizeSendsToSinks(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	sink := &recordingSink{}

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}

	reg.BroadcastResize("sess-1", 120, 40)

	if len(sink.resizes) != 1 || sink.resizes[0] != [2]int{120, 40} {
		t.Fatalf("sink resizes = %#v, want [[120 40]]", sink.resizes)
	}
}

func TestRegistryBroadcastResizeSkipsMissingSession(t *testing.T) {
	reg := NewRegistry()
	// Should not panic
	reg.BroadcastResize("missing", 80, 24)
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
