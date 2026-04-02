package relayserver

import (
	"testing"
	"time"

	"yuanbohan/tunnel/internal/relayapi"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendInput([]byte) error { return nil }
func (fakeAgentPeer) Resize(int, int) error  { return nil }
func (fakeAgentPeer) Close() error           { return nil }

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

func TestRegistryReplaceSessionIDClosesPreviousPeer(t *testing.T) {
	var closed int
	reg := NewRegistry()
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, closingPeer{onClose: func() { closed++ }})
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, closingPeer{onClose: func() { closed += 10 }})

	if closed != 1 {
		t.Fatalf("closed = %d, want 1 old peer close", closed)
	}
}

type closingPeer struct {
	onClose func()
}

func (p closingPeer) SendInput([]byte) error { return nil }
func (p closingPeer) Resize(int, int) error  { return nil }
func (p closingPeer) Close() error {
	if p.onClose != nil {
		p.onClose()
	}
	return nil
}
