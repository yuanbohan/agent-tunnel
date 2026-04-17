package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

type fakeDevicePeer struct {
	mu     sync.Mutex
	sent   []any
	closed int
}

func (p *fakeDevicePeer) SendJSON(v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, v)
	return nil
}

func (p *fakeDevicePeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed++
	return nil
}

func (p *fakeDevicePeer) sentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

func (p *fakeDevicePeer) sentFrame(index int) protocol.DeviceFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.sent) {
		panic("sentFrame: index out of range")
	}
	frame, ok := p.sent[index].(protocol.DeviceFrame)
	if !ok {
		panic("sentFrame: unexpected frame type")
	}
	return frame
}

func (p *fakeDevicePeer) closeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func TestRegistryListsDevicesPerUser(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1", DisplayName: "Mac"}, DeviceOwner{UserID: 1}, &fakeDevicePeer{})
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-2", DisplayName: "Linux"}, DeviceOwner{UserID: 2}, &fakeDevicePeer{})

	devices := registry.ListForUser(1)
	if len(devices) != 1 || devices[0].DeviceID != "dev-1" {
		t.Fatalf("devices = %#v, want only dev-1 for user 1", devices)
	}
}

func TestRegistryLaunchRejectsConcurrentRequestsImmediately(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstCh := make(chan LaunchResult, 1)
	go func() {
		firstCh <- registry.Launch(ctx, "dev-1", 1, "codex", "/repo", "")
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for peer.sentCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for forwarded launch request")
		case <-ticker.C:
		}
	}
	if peer.sentCount() != 1 {
		t.Fatalf("sent count = %d, want one forwarded launch request", peer.sentCount())
	}

	second := registry.Launch(ctx, "dev-1", 1, "claude", "/repo", "")
	if second.Status != LaunchStatusFailed || second.Reason != "busy" || second.RequestID == "" {
		t.Fatalf("second result = %#v, want immediate busy", second)
	}

	frame := peer.sentFrame(0)
	registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusFailed, "busy")
	var first LaunchResult
	select {
	case first = <-firstCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first launch result")
	}
	if first.Status != LaunchStatusFailed || first.Reason != "busy" || first.RequestID == "" {
		t.Fatalf("first result = %#v, want forwarded busy result", first)
	}
}

func TestRegistryLaunchReturnsOfflineForMissingDevice(t *testing.T) {
	registry := NewRegistry()
	result := registry.Launch(context.Background(), "missing", 1, "codex", "/repo", "")
	if result.Status != LaunchStatusFailed || result.Reason != "device_offline" || result.RequestID == "" {
		t.Fatalf("result = %#v, want device_offline", result)
	}
}

func TestRegistryLaunchWaitsForSessionReadyAfterAccepted(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	owner := DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resultCh := make(chan LaunchResult, 1)
	go func() {
		resultCh <- registry.Launch(ctx, "dev-1", 1, "codex", "/repo", "api-fix")
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for peer.sentCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for forwarded launch request")
		case <-ticker.C:
		}
	}

	frame := peer.sentFrame(0)
	if frame.CWD != "/repo" || frame.Label != "api-fix" {
		t.Fatalf("frame = %#v, want forwarded cwd and label", frame)
	}
	if !registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "") {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	if !registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1") {
		t.Fatal("CompleteLaunchIfOwner returned false")
	}

	select {
	case result := <-resultCh:
		if result.Status != LaunchStatusSessionReady || result.SessionID != "sess-1" || result.RequestID != frame.RequestID {
			t.Fatalf("result = %#v, want session_ready sess-1", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for launch result")
	}
}

func TestRegistryLaunchStaysPendingAfterAcceptedDeviceDisconnect(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	owner := DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resultCh := make(chan LaunchResult, 1)
	go func() {
		resultCh <- registry.Launch(ctx, "dev-1", 1, "codex", "/repo", "")
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for peer.sentCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for forwarded launch request")
		case <-ticker.C:
		}
	}

	frame := peer.sentFrame(0)
	if !registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "") {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	if !registry.DisconnectIfOwner("dev-1", peer) {
		t.Fatal("DisconnectIfOwner returned false")
	}
	if !registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1") {
		t.Fatal("CompleteLaunchIfOwner returned false")
	}

	select {
	case result := <-resultCh:
		if result.Status != LaunchStatusSessionReady || result.SessionID != "sess-1" || result.RequestID != frame.RequestID {
			t.Fatalf("result = %#v, want session_ready after disconnect", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for launch result")
	}
}

func TestRegistryLaunchReturnsTimeoutWhenCallerContextEnds(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result := registry.Launch(ctx, "dev-1", 1, "codex", "/repo", "")
	if result.Status != LaunchStatusFailed || result.Reason != "launch_timeout" || result.RequestID == "" {
		t.Fatalf("result = %#v, want launch_timeout", result)
	}
}

func TestRegistryDisconnectAgentTokenDevicesRemovesOnlineDevices(t *testing.T) {
	registry := NewRegistry()
	livePeer := &fakeDevicePeer{}
	pendingPeer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}, livePeer)
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-2"}, DeviceOwner{UserID: 1, AgentTokenID: "agt-2"}, &fakeDevicePeer{})
	registry.RegisterPending(DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}, pendingPeer)

	if got := registry.DisconnectAgentTokenDevices("agt-1"); got != 2 {
		t.Fatalf("DisconnectAgentTokenDevices = %d, want 2", got)
	}
	devices := registry.ListForUser(1)
	if len(devices) != 1 || devices[0].DeviceID != "dev-2" {
		t.Fatalf("devices = %#v, want only dev-2 after disconnect", devices)
	}
	if livePeer.closeCount() != 1 || pendingPeer.closeCount() != 1 {
		t.Fatalf("close counts = live:%d pending:%d, want both peers closed", livePeer.closeCount(), pendingPeer.closeCount())
	}
}

func TestRegistryRegisterOwnedClosesReplacedPeer(t *testing.T) {
	registry := NewRegistry()
	first := &fakeDevicePeer{}
	second := &fakeDevicePeer{}

	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, first)
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, second)

	if first.closeCount() != 1 {
		t.Fatalf("first close count = %d, want 1", first.closeCount())
	}
}

func TestCompleteLaunchRequestIgnoresDuplicateCompletion(t *testing.T) {
	request := &launchRequest{done: make(chan struct{})}

	completeLaunchRequest(request, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
	completeLaunchRequest(request, LaunchResult{Status: LaunchStatusSessionReady})

	<-request.done
	result := request.snapshot()
	if result.Reason != "device_offline" {
		t.Fatalf("result = %#v, want first completion to win", result)
	}
}

func TestRegistryActivatePendingRequiresLivePendingPeer(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	owner := DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}

	if registry.ActivatePending(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, peer) {
		t.Fatal("ActivatePending returned true without pending registration")
	}

	registry.RegisterPending(owner, peer)
	if !registry.ActivatePending(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, peer) {
		t.Fatal("ActivatePending returned false, want true")
	}
}

func TestRegistryActivatePendingRejectsCrossUserDeviceIDReuse(t *testing.T) {
	registry := NewRegistry()
	first := &fakeDevicePeer{}
	second := &fakeDevicePeer{}

	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, first)
	owner := DeviceOwner{UserID: 2, AgentTokenID: "agt-2"}
	registry.RegisterPending(owner, second)

	if registry.ActivatePending(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, second) {
		t.Fatal("ActivatePending returned true for cross-user device_id reuse")
	}
	if first.closeCount() != 0 {
		t.Fatalf("first close count = %d, want 0", first.closeCount())
	}
}

type errDevicePeer struct{ err error }

func (p *errDevicePeer) SendJSON(any) error { return p.err }
func (p *errDevicePeer) Close() error       { return nil }

func TestRegistryClearsInFlightAfterSendFailure(t *testing.T) {
	registry := NewRegistry()
	peer := &errDevicePeer{err: errors.New("boom")}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	result := registry.Launch(context.Background(), "dev-1", 1, "codex", "/repo", "")
	if result.Reason != "device_offline" {
		t.Fatalf("result = %#v, want device_offline", result)
	}

	second := registry.Launch(context.Background(), "dev-1", 1, "claude", "/repo", "")
	if second.Reason != "device_offline" {
		t.Fatalf("second result = %#v, want cleared in-flight state", second)
	}
}
