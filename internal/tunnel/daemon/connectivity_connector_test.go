package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/connectivity/pairtest"
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
