package session

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendJSON(any) error { return nil }
func (fakeAgentPeer) Close() error       { return nil }

type recordingPeer struct {
	closed int
}

func (p *recordingPeer) SendJSON(any) error { return nil }
func (p *recordingPeer) Close() error {
	p.closed++
	return nil
}

type deactivatablePeer struct {
	active bool
}

func (p *deactivatablePeer) SendJSON(any) error {
	if !p.active {
		return ErrAgentPeerInactive
	}
	return nil
}

func (p *deactivatablePeer) Close() error { return nil }
func (p *deactivatablePeer) Deactivate()  { p.active = false }

func TestRegistryRegisterTracksLiveSession(t *testing.T) {
	reg := NewRegistry()
	info := protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: 10}

	reg.Register(info, fakeAgentPeer{})

	if !reg.HasSession("sess-1") {
		t.Fatal("HasSession returned false, want true")
	}
	got, ok := registrySession(reg, "sess-1")
	if !ok || got.Launcher != "codex" || got.StartedAt != 10 {
		t.Fatalf("session = %#v ok=%t, want registered info", got, ok)
	}
}

func TestRegistryTracksOwnerMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, fakeAgentPeer{})

	reg.mu.RLock()
	owner := reg.sessions["sess-1"].owner
	reg.mu.RUnlock()
	if owner.UserID != 1 || owner.AgentTokenID != "agt-1" {
		t.Fatalf("owner = %#v, want user/token owner", owner)
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
	info, ok := registrySession(reg, "sess-1")
	if !ok {
		t.Fatal("registrySession returned false")
	}
	if info.LaunchSource != protocol.SessionLaunchSourceMobile {
		t.Fatalf("LaunchSource = %q, want mobile", info.LaunchSource)
	}
}

func TestRegistryReplaceSessionIDLogsSessionReplacedAndDeactivatesOldOwner(t *testing.T) {
	reg := NewRegistry()
	logs := &bytes.Buffer{}
	restore := logx.UseWriterForTest(logs)
	defer restore()

	oldPeer := &deactivatablePeer{active: true}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, oldPeer)
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})

	if err := oldPeer.SendJSON(protocol.AgentFrame{Type: "ignored"}); err != ErrAgentPeerInactive {
		t.Fatalf("old peer SendJSON error = %v, want ErrAgentPeerInactive", err)
	}
	entries := parseRegistryLogEntries(t, logs.Bytes())
	if len(entries) != 1 {
		t.Fatalf("log entry count = %d, want 1", len(entries))
	}
	if got := entries[0]["event"]; got != "session_replaced" {
		t.Fatalf("event = %v, want session_replaced", got)
	}
}

func TestRegistryDisconnectRemovesSessionImmediately(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	if disconnected := reg.DisconnectIfOwner("sess-1", peer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}
	if reg.HasSession("sess-1") {
		t.Fatal("HasSession returned true after disconnect, want false")
	}
	if peer.closed != 1 {
		t.Fatalf("peer closed = %d, want 1", peer.closed)
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
	info, ok := registrySession(reg, "sess-1")
	if !ok {
		t.Fatal("registrySession returned false, want true")
	}
	if info.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", info.SessionID)
	}
}

func TestRegistryReconnectAfterDisconnectRegistersNewOnlineSession(t *testing.T) {
	reg := NewRegistry()
	oldPeer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: 10}, oldPeer)

	if disconnected := reg.DisconnectIfOwner("sess-1", oldPeer); !disconnected {
		t.Fatal("DisconnectIfOwner returned false, want true")
	}

	newPeer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex", StartedAt: 10}, newPeer)

	info, ok := registrySession(reg, "sess-1")
	if !ok {
		t.Fatal("registrySession returned false after reconnect, want true")
	}
	if info.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", info.SessionID)
	}
}

func TestRegistryDisconnectUserSessionsRemovesOwnedSessions(t *testing.T) {
	reg := NewRegistry()
	ownerA := &recordingPeer{}
	ownerB := &recordingPeer{}

	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-a", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-a"}, ownerA)
	reg.RegisterOwned(protocol.SessionInfo{SessionID: "sess-b", Launcher: "codex"}, SessionOwner{UserID: 2, AgentTokenID: "agt-b"}, ownerB)

	disconnected := reg.DisconnectUserSessions(1, "account_deleted")
	if disconnected != 1 {
		t.Fatalf("DisconnectUserSessions = %d, want 1", disconnected)
	}
	if reg.HasSession("sess-a") {
		t.Fatal("Session sess-a still exists after user disconnect")
	}
	if !reg.HasSession("sess-b") {
		t.Fatal("Session sess-b missing after unrelated user disconnect")
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
	if !reg.HasSession("sess-a") {
		t.Fatal("sess-a missing after unrelated token disconnect")
	}
	if reg.HasSession("sess-b") {
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

func registrySession(reg *Registry, sessionID string) (protocol.SessionInfo, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	live, ok := reg.sessions[sessionID]
	if !ok {
		return protocol.SessionInfo{}, false
	}
	return live.snapshot(), true
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
