package connectivity

import (
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

type recordingPeer struct {
	frames []any
	closed bool
}

func (p *recordingPeer) SendJSON(value any) error {
	p.frames = append(p.frames, value)
	return nil
}

func (p *recordingPeer) Close() error {
	p.closed = true
	return nil
}

func TestRegistryBuildsVisibilityFromDaemonTrustedRoster(t *testing.T) {
	registry := NewRegistry()
	registry.now = func() time.Time { return time.Unix(100, 0) }
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}

	snapshot := registry.RegisterApp(AppOwner{
		UserID:            1,
		AppSessionID:      "appsess-1",
		DeviceFingerprint: "android-a",
	}, appPeer)
	if len(snapshot) != 0 {
		t.Fatalf("snapshot = %#v, want empty before daemon", snapshot)
	}

	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	if len(appPeer.frames) != 1 {
		t.Fatalf("frames = %#v, want daemon upsert", appPeer.frames)
	}
	frame, ok := appPeer.frames[0].(protocol.ConnectivityFrame)
	if !ok || frame.Type != "paired_device_visible" || frame.Daemon.DeviceID != "dev-1" {
		t.Fatalf("frame = %#v, want paired_device_visible dev-1", appPeer.frames[0])
	}
}

func TestRegistryDisconnectAndRevokeNotifyMatchingApps(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	registry.RegisterApp(AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}, appPeer)
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	appPeer.frames = nil

	if !registry.RevokeTrustedAndroid("dev-1", daemonPeer, "android-a") {
		t.Fatal("RevokeTrustedAndroid returned false")
	}
	if len(appPeer.frames) != 1 {
		t.Fatalf("frames after revoke = %#v, want revoke frame", appPeer.frames)
	}
	revoke := appPeer.frames[0].(protocol.ConnectivityFrame)
	if revoke.Type != "paired_device_revoked" {
		t.Fatalf("revoke frame type = %q, want paired_device_revoked", revoke.Type)
	}
	appPeer.frames = nil

	if !registry.DisconnectDaemon("dev-1", daemonPeer) {
		t.Fatal("DisconnectDaemon returned false")
	}
	if len(appPeer.frames) != 0 {
		t.Fatalf("frames after disconnect = %#v, want none after revoked trust", appPeer.frames)
	}
}

func TestRegistryForwardsPairingResponseToReservedDaemon(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}
	owner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterDaemon(owner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)
	if !registry.ReservePairing("dev-1", owner, daemonPeer, "corr-1", time.Minute) {
		t.Fatal("ReservePairing returned false")
	}

	err := registry.ForwardPairingResponse(1, "corr-1", protocol.ConnectivityPairingResponse{
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ForwardPairingResponse returned error: %v", err)
	}
	if len(daemonPeer.frames) != 1 {
		t.Fatalf("frames = %#v, want forwarded response", daemonPeer.frames)
	}
	frame := daemonPeer.frames[0].(protocol.ConnectivityFrame)
	if frame.Type != "pair_response_forward" || frame.PairingResponse.CorrelationID != "corr-1" {
		t.Fatalf("frame = %#v, want pair_response_forward corr-1", frame)
	}
}

func TestRegistryCompletePairingMakesDaemonVisibleToApp(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	registry.RegisterApp(AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}, appPeer)
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)
	if len(appPeer.frames) != 0 {
		t.Fatalf("frames before trust = %#v, want none", appPeer.frames)
	}

	if !registry.CompletePairing("dev-1", daemonPeer, protocol.ConnectivityTrustedAndroid{Fingerprint: "android-a"}) {
		t.Fatal("CompletePairing returned false")
	}
	if len(appPeer.frames) != 1 {
		t.Fatalf("frames = %#v, want visibility update", appPeer.frames)
	}
	frame := appPeer.frames[0].(protocol.ConnectivityFrame)
	if frame.Type != "paired_device_visible" || frame.Daemon.DeviceID != "dev-1" {
		t.Fatalf("frame = %#v, want paired_device_visible dev-1", frame)
	}
}
