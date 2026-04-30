package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"yuanbohan/tunnel/internal/connectivity/direct"
	"yuanbohan/tunnel/internal/connectivity/frame"
	connidentity "yuanbohan/tunnel/internal/connectivity/identity"
	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/connectivity/pairtest"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	conntransport "yuanbohan/tunnel/internal/connectivity/transport"
	"yuanbohan/tunnel/internal/protocol"
)

func TestConnectivityDaemonWebSocketURL(t *testing.T) {
	got, err := connectivityDaemonWebSocketURL("https://relay.example.com/base/")
	if err != nil {
		t.Fatalf("connectivityDaemonWebSocketURL returned error: %v", err)
	}
	if got != "wss://relay.example.com/base/connectivity/daemon/ws" {
		t.Fatalf("url = %q, want connectivity daemon websocket URL", got)
	}
}

func TestConnectivityTunnelWebSocketURL(t *testing.T) {
	got, err := connectivityTunnelWebSocketURL("https://relay.example.com/base/", "tok+123")
	if err != nil {
		t.Fatalf("connectivityTunnelWebSocketURL returned error: %v", err)
	}
	if got != "wss://relay.example.com/base/connectivity/tunnel/ws?token=tok%2B123" {
		t.Fatalf("url = %q, want connectivity tunnel websocket URL", got)
	}
}

func TestConnectivityConnectorRegisterFrameIncludesTrustedRoster(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	fingerprint := strings.Repeat("a", 64)
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: fingerprint,
		DisplayName: "Pixel",
		PairedAt:    1,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := &runtimeState{status: StatusInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		PlatformFamily:    PlatformFamilyMacOS,
		PlatformID:        PlatformFamilyMacOS,
		DaemonFingerprint: identity.Fingerprint,
	}}
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)

	frame, err := connector.registerFrame()
	if err != nil {
		t.Fatalf("registerFrame returned error: %v", err)
	}
	if frame.Type != "daemon_register" || frame.Daemon.DeviceID != "dev-1" {
		t.Fatalf("frame = %#v, want daemon register dev-1", frame)
	}
	if frame.Daemon.DaemonFingerprint != identity.Fingerprint {
		t.Fatalf("DaemonFingerprint = %q, want %q", frame.Daemon.DaemonFingerprint, identity.Fingerprint)
	}
	if len(frame.TrustedDevices) != 1 || frame.TrustedDevices[0].Fingerprint != fingerprint {
		t.Fatalf("TrustedDevices = %#v, want Pixel fingerprint", frame.TrustedDevices)
	}
}

func TestConnectivityConnectorReservesPairingThroughRelayAck(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	server := newConnectivityConnectorTestRelay(t, func(conn *websocket.Conn) {
		var register protocol.ConnectivityFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Errorf("Read register returned error: %v", err)
			return
		}
		var reserve protocol.ConnectivityFrame
		if err := conn.ReadJSON(&reserve); err != nil {
			t.Errorf("Read reserve returned error: %v", err)
			return
		}
		if reserve.Type != "pair_invitation_reserve" || reserve.RequestID != "corr-reserve" {
			t.Errorf("reserve = %#v, want corr-reserve reservation", reserve)
			return
		}
		if err := conn.WriteJSON(protocol.ConnectivityPairInvitationReservedFrame(reserve.RequestID, "1")); err != nil {
			t.Errorf("Write reserved returned error: %v", err)
		}
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := newConnectivityConnector(server.URL, "token", paths, state)
	go connector.Run(ctx)

	reserveCtx, reserveCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reserveCancel()
	accountID, err := state.reservePairing(reserveCtx, "corr-reserve")
	if err != nil {
		t.Fatalf("reservePairing returned error: %v", err)
	}
	if accountID != "1" {
		t.Fatalf("accountID = %q, want 1", accountID)
	}
}

func TestConnectivityConnectorStoresForwardedPairingResponsePendingConfirmation(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	invitation, err := CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev-1",
		DisplayName:    "Laptop",
		AccountID:      "1",
		CorrelationID:  "corr-forward",
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}
	android, err := pairtest.NewAndroidClient("Pixel")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	response, sas, err := android.PairingResponse(connectivityTestInvitation(invitation), "1")
	if err != nil {
		t.Fatalf("PairingResponse returned error: %v", err)
	}

	state := newConnectivityConnectorTestState(paths, identity)
	server := newConnectivityConnectorTestRelay(t, func(conn *websocket.Conn) {
		var register protocol.ConnectivityFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Errorf("Read register returned error: %v", err)
			return
		}
		err := conn.WriteJSON(protocol.ConnectivityPairResponseForwardFrame(protocol.ConnectivityPairingResponse{
			AccountID:          response.AccountID,
			InvitationID:       response.InvitationID,
			CorrelationID:      response.CorrelationID,
			AndroidPublicKey:   response.AndroidPublicKey,
			AndroidFingerprint: response.AndroidFingerprint,
			AndroidDisplayName: response.AndroidDisplayName,
			Signature:          response.Signature,
		}))
		if err != nil {
			t.Errorf("Write pair_response_forward returned error: %v", err)
		}
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := newConnectivityConnector(server.URL, "token", paths, state)
	go connector.Run(ctx)

	var pending []PendingPairingResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err = ListPendingPairingResponses(paths)
		if err != nil {
			t.Fatalf("ListPendingPairingResponses returned error: %v", err)
		}
		if len(pending) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want forwarded response stored", pending)
	}
	if pending[0].SAS != sas || pending[0].AndroidFingerprint != android.Fingerprint {
		t.Fatalf("pending = %#v, want SAS %s for Android", pending[0], sas)
	}
}

func TestConnectivityConnectorDropsStalePairCompletedAfterLocalRevoke(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	fingerprint := strings.Repeat("a", 64)
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: fingerprint,
		DisplayName: "Pixel",
		PairedAt:    1,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	state.connectivityEvents <- protocol.ConnectivityPairCompletedFrame(fingerprint)
	if _, err := RevokeTrustedAndroidDevice(paths, fingerprint); err != nil {
		t.Fatalf("RevokeTrustedAndroidDevice returned error: %v", err)
	}

	checked := make(chan struct{}, 1)
	server := newConnectivityConnectorTestRelay(t, func(conn *websocket.Conn) {
		defer func() {
			select {
			case checked <- struct{}{}:
			default:
			}
		}()
		var register protocol.ConnectivityFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Errorf("Read register returned error: %v", err)
			return
		}
		if len(register.TrustedDevices) != 0 {
			t.Errorf("TrustedDevices = %#v, want revoked device omitted", register.TrustedDevices)
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Errorf("SetReadDeadline returned error: %v", err)
			return
		}
		var event protocol.ConnectivityFrame
		if err := conn.ReadJSON(&event); err == nil {
			t.Errorf("ReadJSON returned event %#v, want stale pair_completed dropped", event)
		}
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := newConnectivityConnector(server.URL, "token", paths, state)
	go connector.Run(ctx)

	select {
	case <-checked:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stale event check")
	}
}

func TestConnectivityConnectorHandlesRendezvousHintWithDirectCandidate(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	android, err := pairtest.NewAndroidClient("Pixel")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: android.Fingerprint,
		PublicKey:   hex.EncodeToString(android.PublicKey),
		DisplayName: "Pixel",
		PairedAt:    1,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)
	connector.stunDiscover = func(context.Context, *direct.UDPSocket) (*net.UDPAddr, error) {
		return &net.UDPAddr{IP: net.IPv4(203, 0, 113, 20), Port: 5000}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var outbound protocol.ConnectivityFrame
	err = connector.handleRendezvousHint(ctx, protocol.ConnectivityFrame{
		Type:               "rendezvous_hint",
		Actor:              "android",
		RequestID:          "request-1",
		AttemptID:          "attempt-1",
		DaemonID:           "dev-1",
		AndroidFingerprint: android.Fingerprint,
		PublicUDPAddr:      "127.0.0.1:9",
		PrivateUDPAddrs:    []string{"127.0.0.1:9"},
	}, func(value any) error {
		var ok bool
		outbound, ok = value.(protocol.ConnectivityFrame)
		if !ok {
			t.Fatalf("outbound value = %#v, want ConnectivityFrame", value)
		}
		cancel()
		return nil
	})
	if err == nil {
		t.Fatal("handleRendezvousHint err = nil, want canceled accept after candidate write")
	}
	if outbound.Type != "rendezvous_hint" || outbound.Actor != "daemon" || outbound.AttemptID != "attempt-1" || outbound.AndroidFingerprint != android.Fingerprint {
		t.Fatalf("outbound = %#v, want daemon rendezvous hint", outbound)
	}
	if outbound.PublicUDPAddr != "203.0.113.20:5000" || outbound.ExpiresAt == 0 {
		t.Fatalf("outbound = %#v, want public address and expiry", outbound)
	}
}

func TestConnectivityConnectorUnregistersFailedDirectAttempt(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	android, err := pairtest.NewAndroidClient("Pixel")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: android.Fingerprint,
		PublicKey:   hex.EncodeToString(android.PublicKey),
		DisplayName: "Pixel",
		PairedAt:    1,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)
	connector.stunDiscover = func(context.Context, *direct.UDPSocket) (*net.UDPAddr, error) {
		return nil, direct.ErrSTUNUnexpectedResponse
	}

	err = connector.handleRendezvousHint(context.Background(), protocol.ConnectivityFrame{
		Type:               "rendezvous_hint",
		Actor:              "android",
		RequestID:          "request-1",
		AttemptID:          "attempt-1",
		DaemonID:           "dev-1",
		AndroidFingerprint: android.Fingerprint,
		PublicUDPAddr:      "127.0.0.1:9",
	}, nil)
	if err == nil {
		t.Fatal("handleRendezvousHint err = nil, want STUN failure")
	}
	connector.directMu.Lock()
	defer connector.directMu.Unlock()
	if len(connector.directAttempts) != 0 {
		t.Fatalf("directAttempts = %#v, want empty after failed direct attempt", connector.directAttempts)
	}
}

func TestConnectivityConnectorHandlesRendezvousHintDirectSuccess(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	android, err := pairtest.NewAndroidClient("Pixel")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: android.Fingerprint,
		PublicKey:   hex.EncodeToString(android.PublicKey),
		DisplayName: "Pixel",
		PairedAt:    1,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)
	connector.stunDiscover = func(_ context.Context, socket *direct.UDPSocket) (*net.UDPAddr, error) {
		local := socket.LocalUDPAddr()
		return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: local.Port}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outboundCh := make(chan protocol.ConnectivityFrame, 3)
	errCh := make(chan error, 1)
	go func() {
		errCh <- connector.handleRendezvousHint(ctx, protocol.ConnectivityFrame{
			Type:               "rendezvous_hint",
			Actor:              "android",
			RequestID:          "request-1",
			AttemptID:          "attempt-1",
			DaemonID:           "dev-1",
			AndroidFingerprint: android.Fingerprint,
			PublicUDPAddr:      "127.0.0.1:9",
			PrivateUDPAddrs:    []string{"127.0.0.1:9"},
		}, func(value any) error {
			frame, ok := value.(protocol.ConnectivityFrame)
			if !ok {
				t.Fatalf("outbound value = %#v, want ConnectivityFrame", value)
			}
			outboundCh <- frame
			return nil
		})
	}()

	var outbound protocol.ConnectivityFrame
	select {
	case outbound = <-outboundCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for daemon rendezvous hint")
	}
	daemonAddr, err := net.ResolveUDPAddr("udp", outbound.PublicUDPAddr)
	if err != nil {
		t.Fatalf("ResolveUDPAddr outbound returned error: %v", err)
	}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP client returned error: %v", err)
	}
	defer packetConn.Close()
	androidCert, err := connidentity.SelfSignedCertificate(android.PrivateKey, connidentity.CertificateOptions{})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}
	quicConn, err := quic.Dial(ctx, packetConn, daemonAddr, conntransport.AndroidTLSConfig(conntransport.EndpointConfig{
		Certificate:         androidCert,
		PinnedPeerPublicKey: identity.PrivateKey.Public().(ed25519.PublicKey),
		ServerName:          "connectivity.daemon",
	}), conntransport.QUICConfig())
	if err != nil {
		t.Fatalf("quic Dial returned error: %v", err)
	}
	control, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		DeviceFingerprint: android.Fingerprint,
		PathKind:          sessionproto.PathDirect,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, control, frame.TypeHello)
	_ = readTestJSONFrame[sessionproto.SessionIndex](t, control, frame.TypeSessionIndex)
	pathState := readTestJSONFrame[sessionproto.PathState](t, control, frame.TypePathState)
	if pathState.PathKind != sessionproto.PathDirect || pathState.AttemptID != "attempt-1" {
		t.Fatalf("pathState = %#v, want direct attempt-1", pathState)
	}
	select {
	case opened := <-outboundCh:
		if opened.Type != "direct_session_open" || opened.AttemptID != "attempt-1" || opened.AndroidFingerprint != android.Fingerprint {
			t.Fatalf("opened = %#v, want direct_session_open attempt-1", opened)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for direct session open")
	}
	connector.cancelDirectAttempt("attempt-1", android.Fingerprint)
	select {
	case err := <-errCh:
		t.Fatalf("direct handler ended after rendezvous_close: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_ = quicConn.CloseWithError(0, "done")
	select {
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			t.Fatalf("handleRendezvousHint returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for direct handler to finish")
	}
	select {
	case closed := <-outboundCh:
		if closed.Type != "direct_session_close" || closed.AttemptID != "attempt-1" || closed.AndroidFingerprint != android.Fingerprint {
			t.Fatalf("closed = %#v, want direct_session_close attempt-1", closed)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for direct session close")
	}
	if got := state.snapshot().LastConnectivityPath; got != "direct" {
		t.Fatalf("LastConnectivityPath = %q, want direct", got)
	}
}

func TestConnectivityConnectorCancelsRendezvousAttempt(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)

	canceled := make(chan struct{})
	connector.registerDirectAttempt("attempt-1", "android-a", func() { close(canceled) })
	connector.cancelDirectAttempt("attempt-1", "android-a")

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for direct attempt cancellation")
	}
}

func TestConnectivityConnectorCancelRendezvousAttemptIsFingerprintScoped(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	state := newConnectivityConnectorTestState(paths, identity)
	connector := newConnectivityConnector("https://relay.example.com", "token", paths, state)

	canceledA := make(chan struct{})
	canceledB := make(chan struct{})
	connector.registerDirectAttempt("attempt-1", "android-a", func() { close(canceledA) })
	connector.registerDirectAttempt("attempt-1", "android-b", func() { close(canceledB) })
	connector.cancelDirectAttempt("attempt-1", "android-b")

	select {
	case <-canceledB:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for android-b cancellation")
	}
	select {
	case <-canceledA:
		t.Fatal("android-a attempt was canceled by android-b close")
	default:
	}
}

func TestRuntimeStateCancelActiveDirectTransportsByFingerprint(t *testing.T) {
	state := &runtimeState{}
	canceledA := make(chan struct{})
	canceledB := make(chan struct{})
	unregisterA := state.registerActiveDirectTransport("attempt-1", "android-a", func() { close(canceledA) })
	defer unregisterA()
	unregisterB := state.registerActiveDirectTransport("attempt-1", "android-b", func() { close(canceledB) })
	defer unregisterB()

	state.cancelActiveDirectTransports("", "android-a")

	select {
	case <-canceledA:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for android-a transport cancellation")
	}
	select {
	case <-canceledB:
		t.Fatal("android-b transport was canceled by android-a revoke")
	default:
	}
}

func TestRuntimeStateCancelPendingDirectAttemptsByFingerprint(t *testing.T) {
	state := &runtimeState{}
	canceledA := make(chan struct{})
	canceledB := make(chan struct{})
	unregisterA := state.registerPendingDirectAttempt("attempt-1", "android-a", func() { close(canceledA) })
	defer unregisterA()
	unregisterB := state.registerPendingDirectAttempt("attempt-1", "android-b", func() { close(canceledB) })
	defer unregisterB()

	state.cancelPendingDirectAttempts("", "android-a")

	select {
	case <-canceledA:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for android-a pending attempt cancellation")
	}
	select {
	case <-canceledB:
		t.Fatal("android-b pending attempt was canceled by android-a revoke")
	default:
	}
}

func newConnectivityConnectorTestState(paths Paths, identity ConnectivityIdentity) *runtimeState {
	return &runtimeState{
		status: StatusInfo{
			DeviceID:          "dev-1",
			DisplayName:       "Laptop",
			PlatformFamily:    PlatformFamilyMacOS,
			PlatformID:        PlatformFamilyMacOS,
			DaemonFingerprint: identity.Fingerprint,
		},
		paths:               paths,
		connectivityEvents:  make(chan protocol.ConnectivityFrame, 16),
		connectivityReplies: make(map[string]chan protocol.ConnectivityFrame),
	}
}

func newConnectivityConnectorTestRelay(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
}

func connectivityTestInvitation(invitation PairInvitation) connectivitypairing.Invitation {
	return connectivitypairing.Invitation{
		Version:           connectivitypairing.Version,
		AccountID:         invitation.AccountID,
		DaemonID:          invitation.DeviceID,
		DaemonDisplayName: invitation.DisplayName,
		DaemonPublicKey:   invitation.DaemonPublicKey,
		DaemonFingerprint: invitation.DaemonFingerprint,
		InvitationID:      invitation.InvitationID,
		CorrelationID:     invitation.CorrelationID,
		Nonce:             invitation.Nonce,
		ExpiresAt:         invitation.ExpiresAt,
		RelayBaseURL:      invitation.RelayBaseURL,
		Signature:         invitation.Signature,
	}
}
