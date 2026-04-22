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
	registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusFailed, "busy", "")
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
	if _, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "", "launch_fixed"); !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	target, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1")
	if !ok {
		t.Fatal("CompleteLaunchIfOwner returned false")
	}
	if target.DeviceID != "dev-1" || target.WorkspaceSession != "launch_fixed" {
		t.Fatalf("target = %#v, want dev-1 launch_fixed", target)
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

func TestRegistryLaunchWaitsForAcceptedAfterSessionReady(t *testing.T) {
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
	if target, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1"); ok || target != (TerminateTarget{}) {
		t.Fatalf("CompleteLaunchIfOwner = %#v, %v, want pending without target", target, ok)
	}
	select {
	case result := <-resultCh:
		t.Fatalf("launch completed before accepted result: %#v", result)
	default:
	}

	completion, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "", "launch_fixed")
	if !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for late accepted result")
	}
	if completion.SessionID != "sess-1" || completion.Target.DeviceID != "dev-1" || completion.Target.WorkspaceSession != "launch_fixed" {
		t.Fatalf("completion = %#v, want sess-1 dev-1 launch_fixed", completion)
	}

	select {
	case result := <-resultCh:
		if result.Status != LaunchStatusSessionReady || result.SessionID != "sess-1" || result.RequestID != frame.RequestID {
			t.Fatalf("result = %#v, want session_ready sess-1", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for launch result")
	}

	cached, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1")
	if !ok || cached.DeviceID != "dev-1" || cached.WorkspaceSession != "launch_fixed" {
		t.Fatalf("cached target = %#v, %v, want dev-1 launch_fixed", cached, ok)
	}
}

func TestRegistryCompleteLaunchIfOwnerMarksLaunchWithoutWorkspaceTarget(t *testing.T) {
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
	if _, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "", ""); !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	target, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1")
	if !ok {
		t.Fatal("CompleteLaunchIfOwner returned false for accepted launch without workspace target")
	}
	if target.DeviceID != "dev-1" || target.WorkspaceSession != "" {
		t.Fatalf("target = %#v, want dev-1 with empty workspace target", target)
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

func TestRegistryResolveLaunchIfOwnerDefaultsMissingFailureReason(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

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
	if _, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusFailed, "   ", ""); !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for failed result")
	}

	select {
	case result := <-resultCh:
		if result.Status != LaunchStatusFailed || result.Reason != "unknown_reason" || result.RequestID != frame.RequestID {
			t.Fatalf("result = %#v, want unknown_reason", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for launch result")
	}
}

func TestRegistryCompleteLaunchIfOwnerRejectsBlankSessionID(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	owner := DeviceOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, owner, peer)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
	if _, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "", ""); !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	if _, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "   "); ok {
		t.Fatal("CompleteLaunchIfOwner returned true for blank session id")
	}

	select {
	case result := <-resultCh:
		if result.Status != LaunchStatusFailed || result.Reason != "launch_timeout" {
			t.Fatalf("result = %#v, want launch_timeout after blank session id", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for launch timeout")
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
	if _, ok := registry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, LaunchStatusAccepted, "", "launch_fixed"); !ok {
		t.Fatal("ResolveLaunchIfOwner returned false for accepted result")
	}
	if !registry.DisconnectIfOwner("dev-1", peer) {
		t.Fatal("DisconnectIfOwner returned false")
	}
	if _, ok := registry.CompleteLaunchIfOwner(frame.RequestID, owner, "sess-1"); !ok {
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

func TestRegistryTerminateRoutesToOwningDevice(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resultCh := make(chan TerminateResult, 1)
	go func() {
		resultCh <- registry.Terminate(ctx, 1, "sess-1", TerminateTarget{
			DeviceID:         "dev-1",
			WorkspaceSession: "launch_fixed",
		})
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for peer.sentCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for forwarded terminate request")
		case <-ticker.C:
		}
	}

	frame := peer.sentFrame(0)
	if frame.Type != "terminate_request" || frame.SessionID != "sess-1" || frame.WorkspaceSession != "launch_fixed" {
		t.Fatalf("frame = %#v, want terminate_request sess-1 launch_fixed", frame)
	}
	if !registry.ResolveTerminateIfOwner("dev-1", peer, frame.RequestID, TerminateStatusTerminated, "") {
		t.Fatal("ResolveTerminateIfOwner returned false for terminated result")
	}

	select {
	case result := <-resultCh:
		if result.Status != TerminateStatusTerminated || result.RequestID != frame.RequestID || result.Reason != "" {
			t.Fatalf("result = %#v, want terminated", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminate result")
	}
}

func TestRegistryTerminateReturnsOfflineForMissingDevice(t *testing.T) {
	registry := NewRegistry()
	result := registry.Terminate(context.Background(), 1, "sess-1", TerminateTarget{
		DeviceID:         "dev-1",
		WorkspaceSession: "launch_fixed",
	})
	if result.Status != TerminateStatusFailed || result.Reason != "device_offline" || result.RequestID == "" {
		t.Fatalf("result = %#v, want device_offline", result)
	}
}

func TestRegistryTerminateRejectsMissingWorkspaceTarget(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	result := registry.Terminate(context.Background(), 1, "sess-1", TerminateTarget{
		DeviceID: "dev-1",
	})
	if result.Status != TerminateStatusFailed || result.Reason != "session_not_found" {
		t.Fatalf("result = %#v, want session_not_found", result)
	}
	if peer.sentCount() != 0 {
		t.Fatalf("sent count = %d, want no forwarded terminate request", peer.sentCount())
	}
}

func TestRegistryTerminateReturnsTimeoutAndClearsRequest(t *testing.T) {
	registry := NewRegistry()
	peer := &fakeDevicePeer{}
	registry.RegisterOwned(protocol.DeviceInfo{DeviceID: "dev-1"}, DeviceOwner{UserID: 1}, peer)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result := registry.Terminate(ctx, 1, "sess-1", TerminateTarget{
		DeviceID:         "dev-1",
		WorkspaceSession: "launch_fixed",
	})
	if result.Status != TerminateStatusFailed || result.Reason != "terminate_timeout" || result.RequestID == "" {
		t.Fatalf("result = %#v, want terminate_timeout", result)
	}
	if peer.sentCount() != 1 {
		t.Fatalf("sent count = %d, want forwarded terminate request", peer.sentCount())
	}
	if registry.ResolveTerminateIfOwner("dev-1", peer, result.RequestID, TerminateStatusTerminated, "") {
		t.Fatal("ResolveTerminateIfOwner returned true after timeout cleanup")
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
