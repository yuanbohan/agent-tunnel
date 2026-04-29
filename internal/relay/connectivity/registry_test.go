package connectivity

import (
	"errors"
	"strconv"
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

func TestRegistryRegisterAppIfValidRejectsRevokedSessionUnderLock(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}

	snapshot, ok := registry.RegisterAppIfValid(AppOwner{
		UserID:            1,
		AppSessionID:      "appsess-1",
		DeviceFingerprint: "android-a",
	}, appPeer, func() bool { return false })
	if ok || len(snapshot) != 0 {
		t.Fatalf("RegisterAppIfValid snapshot=%#v ok=%v, want rejected", snapshot, ok)
	}
	if removed := registry.DisconnectAppSession("appsess-1", "logged_out"); removed != 0 {
		t.Fatalf("DisconnectAppSession removed %d peers, want 0", removed)
	}
}

func TestRegistryRegisterAppIfValidRejectsDisconnectAfterFinalAuth(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}

	_, ok := registry.RegisterAppIfValid(AppOwner{
		UserID:            1,
		AppSessionID:      "appsess-1",
		DeviceFingerprint: "android-a",
	}, appPeer, func() bool {
		if removed := registry.DisconnectAppSession("appsess-1", "logged_out"); removed != 0 {
			t.Fatalf("DisconnectAppSession removed %d peers, want 0", removed)
		}
		return true
	})
	if ok {
		t.Fatal("RegisterAppIfValid accepted an app session disconnected after final auth")
	}
	if appPeer.closed {
		t.Fatal("app peer was closed despite never being registered")
	}
}

func TestRegistryRegisterDaemonIfValidRejectsRevokedTokenUnderLock(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}

	appPeers, ok := registry.RegisterDaemonIfValid(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer, func() bool { return false })
	if ok || len(appPeers) != 0 {
		t.Fatalf("RegisterDaemonIfValid appPeers=%#v ok=%v, want rejected", appPeers, ok)
	}
	if removed := registry.DisconnectAgentToken("agt-1"); removed != 0 {
		t.Fatalf("DisconnectAgentToken removed %d peers, want 0", removed)
	}
}

func TestRegistryRegisterDaemonIfValidRejectsTokenRevokedAfterFinalAuth(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}

	_, ok := registry.RegisterDaemonIfValid(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer, func() bool {
		if removed := registry.DisconnectAgentToken("agt-1"); removed != 0 {
			t.Fatalf("DisconnectAgentToken removed %d peers, want 0", removed)
		}
		return true
	})
	if ok {
		t.Fatal("RegisterDaemonIfValid accepted an agent token revoked after final auth")
	}
	if daemonPeer.closed {
		t.Fatal("daemon peer was closed despite never being registered")
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

func TestRegistryRejectsPairingResponseFromDisconnectedAppPeer(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonOwner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(daemonOwner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)
	if !registry.ReservePairing("dev-1", daemonOwner, daemonPeer, "corr-1", time.Minute) {
		t.Fatal("ReservePairing returned false")
	}
	registry.DisconnectAppSession("appsess-1", "logged_out")

	err := registry.ForwardPairingResponseFromApp(appOwner, appPeer, "corr-1", protocol.ConnectivityPairingResponse{
		CorrelationID: "corr-1",
	})
	if !errors.Is(err, ErrPairingCorrelationNotFound) {
		t.Fatalf("ForwardPairingResponseFromApp err = %v, want ErrPairingCorrelationNotFound", err)
	}
	if len(daemonPeer.frames) != 0 {
		t.Fatalf("daemon frames = %#v, want no forwarding after app disconnect", daemonPeer.frames)
	}
}

func TestRegistryKeepsPairingCorrelationsIsolatedByUser(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}
	owner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterDaemon(owner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)
	if !registry.ReservePairing("dev-1", owner, daemonPeer, "corr-shared", time.Minute) {
		t.Fatal("ReservePairing returned false")
	}

	err := registry.ForwardPairingResponse(2, "corr-shared", protocol.ConnectivityPairingResponse{
		CorrelationID: "corr-shared",
	})
	if !errors.Is(err, ErrPairingCorrelationNotFound) {
		t.Fatalf("wrong-user ForwardPairingResponse err = %v, want ErrPairingCorrelationNotFound", err)
	}
	if len(daemonPeer.frames) != 0 {
		t.Fatalf("frames after wrong-user response = %#v, want none", daemonPeer.frames)
	}

	err = registry.ForwardPairingResponse(1, "corr-shared", protocol.ConnectivityPairingResponse{
		CorrelationID: "corr-shared",
	})
	if err != nil {
		t.Fatalf("owner ForwardPairingResponse returned error after wrong-user attempt: %v", err)
	}
	if len(daemonPeer.frames) != 1 {
		t.Fatalf("frames = %#v, want owner response forwarded", daemonPeer.frames)
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

func TestRegistryForwardsRendezvousHintsForVisibleDaemon(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonOwner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(daemonOwner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	appPeer.frames = nil
	daemonPeer.frames = nil

	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-1", "request-1", "203.0.113.1:5000", []string{"10.0.0.1:5000"}, time.Minute); err != nil {
		t.Fatalf("OpenRendezvousFromApp returned error: %v", err)
	}
	if len(daemonPeer.frames) != 1 {
		t.Fatalf("daemon frames = %#v, want rendezvous hint", daemonPeer.frames)
	}
	daemonFrame := daemonPeer.frames[0].(protocol.ConnectivityFrame)
	if daemonFrame.Type != "rendezvous_hint" || daemonFrame.Actor != TunnelActorAndroid || daemonFrame.AttemptID != "attempt-1" || daemonFrame.PublicUDPAddr != "203.0.113.1:5000" {
		t.Fatalf("daemon frame = %#v, want app rendezvous_hint", daemonFrame)
	}
	if daemonFrame.ExpiresAt != now.Add(time.Minute).Unix() {
		t.Fatalf("ExpiresAt = %d, want %d", daemonFrame.ExpiresAt, now.Add(time.Minute).Unix())
	}

	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-1", "request-2", "203.0.113.2:6000", []string{"10.0.0.2:6000"}); err != nil {
		t.Fatalf("ForwardRendezvousHintFromDaemon returned error: %v", err)
	}
	if len(appPeer.frames) != 1 {
		t.Fatalf("app frames = %#v, want daemon rendezvous hint", appPeer.frames)
	}
	appFrame := appPeer.frames[0].(protocol.ConnectivityFrame)
	if appFrame.Type != "rendezvous_hint" || appFrame.Actor != TunnelActorDaemon || appFrame.AttemptID != "attempt-1" || appFrame.PublicUDPAddr != "203.0.113.2:6000" {
		t.Fatalf("app frame = %#v, want daemon rendezvous_hint", appFrame)
	}
}

func TestRegistryRendezvousCloseExpiryAndSupersedeRejectStaleHints(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonOwner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(daemonOwner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)

	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-1", "request-1", "203.0.113.1:5000", nil, time.Minute); err != nil {
		t.Fatalf("OpenRendezvousFromApp attempt-1 returned error: %v", err)
	}
	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-2", "request-2", "203.0.113.1:5001", nil, time.Minute); err != nil {
		t.Fatalf("OpenRendezvousFromApp attempt-2 returned error: %v", err)
	}
	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-1", "request-stale", "203.0.113.2:6000", nil); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("stale ForwardRendezvousHintFromDaemon err = %v, want ErrRendezvousUnavailable", err)
	}
	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-2", "request-current", "203.0.113.2:6000", nil); err != nil {
		t.Fatalf("current ForwardRendezvousHintFromDaemon returned error: %v", err)
	}

	if !registry.CloseRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-2") {
		t.Fatal("CloseRendezvousFromApp returned false")
	}
	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-2", "request-closed", "203.0.113.2:6000", nil); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("closed ForwardRendezvousHintFromDaemon err = %v, want ErrRendezvousUnavailable", err)
	}

	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-3", "request-3", "203.0.113.1:5002", nil, time.Second); err != nil {
		t.Fatalf("OpenRendezvousFromApp attempt-3 returned error: %v", err)
	}
	now = now.Add(2 * time.Second)
	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-3", "request-expired", "203.0.113.2:6000", nil); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("expired ForwardRendezvousHintFromDaemon err = %v, want ErrRendezvousUnavailable", err)
	}
}

func TestRegistryRendezvousRejectsUnpairedDisconnectedAndMalformedAttempts(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)

	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-1", "request-1", "203.0.113.1:5000", nil, time.Minute); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("unpaired OpenRendezvousFromApp err = %v, want ErrRendezvousUnavailable", err)
	}
	registry.CompletePairing("dev-1", daemonPeer, protocol.ConnectivityTrustedAndroid{Fingerprint: "android-a"})
	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "", "request-2", "203.0.113.1:5000", nil, time.Minute); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("missing attempt OpenRendezvousFromApp err = %v, want ErrRendezvousUnavailable", err)
	}
	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-2", "request-3", "not-an-addr", nil, time.Minute); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("malformed OpenRendezvousFromApp err = %v, want ErrRendezvousUnavailable", err)
	}
	registry.DisconnectAppSession("appsess-1", "logged_out")
	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-3", "request-4", "203.0.113.1:5000", nil, time.Minute); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("disconnected OpenRendezvousFromApp err = %v, want ErrRendezvousUnavailable", err)
	}
}

func TestRegistryRateLimitsRendezvousOpensPerAccount(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)

	for i := 0; i < RelayTunnelRequestLimit; i++ {
		suffix := strconv.Itoa(i)
		appPeer := &recordingPeer{}
		owner := AppOwner{UserID: 1, AppSessionID: "appsess-" + suffix, DeviceFingerprint: "android-a"}
		registry.RegisterApp(owner, appPeer)
		if _, err := registry.OpenRendezvousFromApp(owner, appPeer, "dev-1", "attempt-"+suffix, "request", "203.0.113.1:5000", nil, time.Minute); err != nil {
			t.Fatalf("OpenRendezvousFromApp %d returned error: %v", i, err)
		}
	}
	appPeer := &recordingPeer{}
	owner := AppOwner{UserID: 1, AppSessionID: "appsess-over", DeviceFingerprint: "android-a"}
	registry.RegisterApp(owner, appPeer)
	if _, err := registry.OpenRendezvousFromApp(owner, appPeer, "dev-1", "attempt-over", "request", "203.0.113.1:5000", nil, time.Minute); !errors.Is(err, ErrRendezvousRateLimited) {
		t.Fatalf("over-limit OpenRendezvousFromApp err = %v, want ErrRendezvousRateLimited", err)
	}

	now = now.Add(RelayTunnelRequestWindow + time.Second)
	if _, err := registry.OpenRendezvousFromApp(owner, appPeer, "dev-1", "attempt-after-window", "request", "203.0.113.1:5000", nil, time.Minute); err != nil {
		t.Fatalf("OpenRendezvousFromApp after window returned error: %v", err)
	}
}

func TestRegistryRendezvousRevocationRemovesAttemptState(t *testing.T) {
	registry := NewRegistry()
	appPeer := &recordingPeer{}
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonOwner := DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(daemonOwner, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	if _, err := registry.OpenRendezvousFromApp(appOwner, appPeer, "dev-1", "attempt-1", "request-1", "203.0.113.1:5000", nil, time.Minute); err != nil {
		t.Fatalf("OpenRendezvousFromApp returned error: %v", err)
	}

	if !registry.RevokeTrustedAndroid("dev-1", daemonPeer, "android-a") {
		t.Fatal("RevokeTrustedAndroid returned false")
	}
	if err := registry.ForwardRendezvousHintFromDaemon(daemonOwner, "dev-1", daemonPeer, "attempt-1", "request-2", "203.0.113.2:6000", nil); !errors.Is(err, ErrRendezvousUnavailable) {
		t.Fatalf("ForwardRendezvousHintFromDaemon after revoke err = %v, want ErrRendezvousUnavailable", err)
	}
}

func TestRegistryIssuesAndRedeemsRelayTunnelTokensForVisibleDaemon(t *testing.T) {
	registry := NewRegistry()
	registry.now = func() time.Time { return time.Unix(100, 0) }
	daemonPeer := &recordingPeer{}
	appPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	daemonPeer.frames = nil

	appFrame, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-1", "request-1", time.Minute)
	if err != nil {
		t.Fatalf("RequestRelayTunnel returned error: %v", err)
	}
	if appFrame.Type != "relay_tunnel_ready" || appFrame.Actor != TunnelActorAndroid || appFrame.AttemptID != "attempt-1" || appFrame.TunnelToken == "" {
		t.Fatalf("app frame = %#v, want android relay_tunnel_ready", appFrame)
	}
	if len(daemonPeer.frames) != 1 {
		t.Fatalf("daemon frames = %#v, want daemon relay_tunnel_ready", daemonPeer.frames)
	}
	daemonFrame := daemonPeer.frames[0].(protocol.ConnectivityFrame)
	if daemonFrame.Type != "relay_tunnel_ready" || daemonFrame.Actor != TunnelActorDaemon || daemonFrame.AttemptID != "attempt-1" || daemonFrame.TunnelToken == "" {
		t.Fatalf("daemon frame = %#v, want daemon relay_tunnel_ready", daemonFrame)
	}
	if daemonFrame.TunnelToken == appFrame.TunnelToken {
		t.Fatal("daemon and app tokens match, want actor-specific tokens")
	}

	appRedemption, err := registry.RedeemRelayTunnelToken(appFrame.TunnelToken)
	if err != nil {
		t.Fatalf("RedeemRelayTunnelToken app returned error: %v", err)
	}
	if appRedemption.Actor != TunnelActorAndroid || appRedemption.AttemptID != "attempt-1" || appRedemption.TunnelKey == "" || appRedemption.DeviceFingerprint != "android-a" {
		t.Fatalf("app redemption = %#v, want android attempt redemption", appRedemption)
	}
	daemonRedemption, err := registry.RedeemRelayTunnelToken(daemonFrame.TunnelToken)
	if err != nil {
		t.Fatalf("RedeemRelayTunnelToken daemon returned error: %v", err)
	}
	if daemonRedemption.Actor != TunnelActorDaemon || daemonRedemption.DaemonDeviceID != "dev-1" {
		t.Fatalf("daemon redemption = %#v, want daemon attempt redemption", daemonRedemption)
	}
	if _, err := registry.RedeemRelayTunnelToken(appFrame.TunnelToken); !errors.Is(err, ErrRelayTunnelTokenInvalid) {
		t.Fatalf("second redeem err = %v, want ErrRelayTunnelTokenInvalid", err)
	}
}

func TestRegistryRejectsRelayTunnelRequestFromDisconnectedAppPeer(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}
	appPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	registry.RegisterApp(appOwner, appPeer)
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	registry.DisconnectAppSession("appsess-1", "logged_out")

	if _, err := registry.RequestRelayTunnelFromApp(appOwner, appPeer, "dev-1", "attempt-1", "request-1", time.Minute); !errors.Is(err, ErrRelayTunnelUnavailable) {
		t.Fatalf("RequestRelayTunnelFromApp err = %v, want ErrRelayTunnelUnavailable", err)
	}
	if len(daemonPeer.frames) != 0 {
		t.Fatalf("daemon frames = %#v, want no relay tunnel frame after app disconnect", daemonPeer.frames)
	}
}

func TestRegistryRateLimitsRelayTunnelRequestsPerAccount(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)

	for i := 0; i < RelayTunnelRequestLimit; i++ {
		suffix := strconv.Itoa(i)
		owner := AppOwner{UserID: 1, AppSessionID: "appsess-" + suffix, DeviceFingerprint: "android-a"}
		if _, err := registry.RequestRelayTunnel(owner, "dev-1", "attempt-"+suffix, "request", time.Minute); err != nil {
			t.Fatalf("RequestRelayTunnel %d returned error: %v", i, err)
		}
	}
	owner := AppOwner{UserID: 1, AppSessionID: "appsess-over", DeviceFingerprint: "android-a"}
	if _, err := registry.RequestRelayTunnel(owner, "dev-1", "attempt-over", "request", time.Minute); !errors.Is(err, ErrRelayTunnelRateLimited) {
		t.Fatalf("over-limit RequestRelayTunnel err = %v, want ErrRelayTunnelRateLimited", err)
	}

	now = now.Add(RelayTunnelRequestWindow + time.Second)
	if _, err := registry.RequestRelayTunnel(owner, "dev-1", "attempt-after-window", "request", time.Minute); err != nil {
		t.Fatalf("RequestRelayTunnel after window returned error: %v", err)
	}
}

func TestRegistryCapsRelayTunnelRequestsPerAppSession(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)

	for i := 0; i < RelayTunnelInFlightPerAppSessionLimit; i++ {
		if _, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-"+strconv.Itoa(i), "request", time.Minute); err != nil {
			t.Fatalf("RequestRelayTunnel %d returned error: %v", i, err)
		}
	}
	if _, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-over", "request", time.Minute); !errors.Is(err, ErrRelayTunnelRateLimited) {
		t.Fatalf("over in-flight RequestRelayTunnel err = %v, want ErrRelayTunnelRateLimited", err)
	}
}

func TestRegistryRejectsRelayTunnelForUnpairedOrExpiredAttempt(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, nil, daemonPeer)

	if _, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-1", "request-1", time.Minute); !errors.Is(err, ErrRelayTunnelUnavailable) {
		t.Fatalf("unpaired RequestRelayTunnel err = %v, want ErrRelayTunnelUnavailable", err)
	}
	registry.CompletePairing("dev-1", daemonPeer, protocol.ConnectivityTrustedAndroid{Fingerprint: "android-a"})
	appFrame, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-2", "request-2", time.Second)
	if err != nil {
		t.Fatalf("RequestRelayTunnel returned error: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := registry.RedeemRelayTunnelToken(appFrame.TunnelToken); !errors.Is(err, ErrRelayTunnelTokenInvalid) {
		t.Fatalf("expired redeem err = %v, want ErrRelayTunnelTokenInvalid", err)
	}
}

func TestRegistrySweepsExpiredUnpairedRelayTunnelAttempts(t *testing.T) {
	registry := NewRegistry()
	now := time.Unix(100, 0)
	registry.now = func() time.Time { return now }
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	appFrame, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-1", "request-1", time.Second)
	if err != nil {
		t.Fatalf("RequestRelayTunnel returned error: %v", err)
	}

	now = now.Add(2 * time.Second)
	if removed := registry.SweepExpiredRelayTunnelAttempts(); removed != 1 {
		t.Fatalf("SweepExpiredRelayTunnelAttempts = %d, want 1", removed)
	}
	if _, err := registry.RedeemRelayTunnelToken(appFrame.TunnelToken); !errors.Is(err, ErrRelayTunnelTokenInvalid) {
		t.Fatalf("redeem after sweep err = %v, want ErrRelayTunnelTokenInvalid", err)
	}
}

func TestRegistryKeepsSameAttemptIDIsolatedByAppSession(t *testing.T) {
	registry := NewRegistry()
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	firstOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	secondOwner := AppOwner{UserID: 1, AppSessionID: "appsess-2", DeviceFingerprint: "android-a"}
	firstFrame, err := registry.RequestRelayTunnel(firstOwner, "dev-1", "attempt-shared", "request-1", time.Minute)
	if err != nil {
		t.Fatalf("first RequestRelayTunnel returned error: %v", err)
	}
	secondFrame, err := registry.RequestRelayTunnel(secondOwner, "dev-1", "attempt-shared", "request-2", time.Minute)
	if err != nil {
		t.Fatalf("second RequestRelayTunnel returned error: %v", err)
	}
	firstRedemption, err := registry.RedeemRelayTunnelToken(firstFrame.TunnelToken)
	if err != nil {
		t.Fatalf("first RedeemRelayTunnelToken returned error: %v", err)
	}
	secondRedemption, err := registry.RedeemRelayTunnelToken(secondFrame.TunnelToken)
	if err != nil {
		t.Fatalf("second RedeemRelayTunnelToken returned error: %v", err)
	}
	if firstRedemption.TunnelKey == secondRedemption.TunnelKey {
		t.Fatalf("TunnelKey matched for distinct app sessions: %q", firstRedemption.TunnelKey)
	}
}

func TestRegistryClosesRelayTunnelOnRevocation(t *testing.T) {
	registry := NewRegistry()
	appOwner := AppOwner{UserID: 1, AppSessionID: "appsess-1", DeviceFingerprint: "android-a"}
	daemonPeer := &recordingPeer{}
	registry.RegisterDaemon(DaemonOwner{UserID: 1, AgentTokenID: "agt-1"}, protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonFingerprint: "daemon-a",
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: "android-a"}}, daemonPeer)
	appFrame, err := registry.RequestRelayTunnel(appOwner, "dev-1", "attempt-1", "request-1", time.Minute)
	if err != nil {
		t.Fatalf("RequestRelayTunnel returned error: %v", err)
	}
	redemption, err := registry.RedeemRelayTunnelToken(appFrame.TunnelToken)
	if err != nil {
		t.Fatalf("RedeemRelayTunnelToken returned error: %v", err)
	}
	closed := false
	registry.AddTunnelCloser(redemption.TunnelKey, func() { closed = true })

	if !registry.RevokeTrustedAndroid("dev-1", daemonPeer, "android-a") {
		t.Fatal("RevokeTrustedAndroid returned false")
	}
	if !closed {
		t.Fatal("tunnel closer was not called on revoke")
	}
}
