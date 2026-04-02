package relayserver

import (
	"bytes"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/relayapi"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendInput([]byte) error { return nil }
func (fakeAgentPeer) Resize(int, int) error  { return nil }
func (fakeAgentPeer) Close() error           { return nil }

type recordingPeer struct {
	inputs  [][]byte
	resizes [][2]int
	closed  int
}

func (p *recordingPeer) SendInput(data []byte) error {
	p.inputs = append(p.inputs, append([]byte(nil), data...))
	return nil
}

func (p *recordingPeer) Resize(cols, rows int) error {
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}

func (p *recordingPeer) Close() error {
	p.closed++
	return nil
}

type recordingSink struct {
	chunks [][]byte
}

func (s *recordingSink) WriteOutput(data []byte) error {
	s.chunks = append(s.chunks, append([]byte(nil), data...))
	return nil
}

func TestRegistryRegisterAndListSortedByLastActive(t *testing.T) {
	reg := NewRegistry()
	olderActive := time.Unix(20, 0)
	newerActive := time.Unix(30, 0)
	older := relayapi.SessionInfo{SessionID: "a", Launcher: "codex", StartedAt: time.Unix(10, 0), LastActiveAt: &olderActive}
	newer := relayapi.SessionInfo{SessionID: "b", Launcher: "gemini", StartedAt: time.Unix(11, 0), LastActiveAt: &newerActive}

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
	if err := reg.Resize("missing", 80, 24); err != ErrSessionNotFound {
		t.Fatalf("Resize error = %v, want ErrSessionNotFound", err)
	}
}

func TestRegistryTouchOutputUpdatesPreviewAndLastActive(t *testing.T) {
	reg := NewRegistry()
	info := relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: time.Unix(10, 0)}
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

	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
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

	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)
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

func TestRegistryRemoveIfOwnerSkipsStaleOwnerAfterReplacement(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	newPeer := &recordingPeer{}

	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

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
	sink := &recordingSink{}

	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	if err := reg.AddSink("sess-1", "browser", sink); err != nil {
		t.Fatalf("AddSink returned error: %v", err)
	}
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, newPeer)

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
