package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/connectivity/pairtest"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/daemon"
)

func TestConnectivityWebSocketsExposeTrustedDaemonSnapshot(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	appFingerprint := strings.Repeat("a", 64)
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", appFingerprint)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   strings.Repeat("b", 64),
		DaemonFingerprint: strings.Repeat("c", 64),
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: appFingerprint, DisplayName: "Pixel"}})); err != nil {
		t.Fatalf("daemon WriteJSON returned error: %v", err)
	}

	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app WriteJSON returned error: %v", err)
	}
	var snapshot protocol.ConnectivityFrame
	if err := appConn.ReadJSON(&snapshot); err != nil {
		t.Fatalf("app ReadJSON returned error: %v", err)
	}
	if snapshot.Type != "daemon_snapshot" || len(snapshot.Daemons) != 1 || snapshot.Daemons[0].DeviceID != "dev-1" {
		t.Fatalf("snapshot = %#v, want dev-1", snapshot)
	}

	if err := daemonConn.WriteJSON(protocol.ConnectivityFrame{
		Type:               "paired_device_revoked",
		AndroidFingerprint: appFingerprint,
	}); err != nil {
		t.Fatalf("daemon revoke WriteJSON returned error: %v", err)
	}
	var revoked protocol.ConnectivityFrame
	if err := appConn.ReadJSON(&revoked); err != nil {
		t.Fatalf("app revoke ReadJSON returned error: %v", err)
	}
	if revoked.Type != "paired_device_revoked" || revoked.Daemon.DeviceID != "dev-1" {
		t.Fatalf("revoked = %#v, want paired_device_revoked dev-1", revoked)
	}
}

func TestConnectivityAppWebSocketRequiresFingerprintBoundSession(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	appSession, err := env.appAuth.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/connectivity/app/ws"

	headers := http.Header{"Authorization": {"Bearer " + appSession.AccessToken}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("Dial error = nil, want fingerprint-bound rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 403", status)
	}
}

func TestConnectivityAppWebSocketClosesOnLogout(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	var snapshot protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &snapshot)
	if snapshot.Type != "daemon_snapshot" {
		t.Fatalf("snapshot = %#v, want daemon_snapshot", snapshot)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", bearerAuth(appSession.AccessToken))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	assertConnectivityClosed(t, appConn)
}

func TestConnectivityAppWebSocketCannotRegisterAfterLogout(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", bearerAuth(appSession.AccessToken))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	assertConnectivityClosed(t, appConn)
}

func TestConnectivityRelayTunnelForwardsOpaqueBinaryPackets(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	appFingerprint := strings.Repeat("a", 64)
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", appFingerprint)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   strings.Repeat("b", 64),
		DaemonFingerprint: strings.Repeat("c", 64),
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: appFingerprint, DisplayName: "Pixel"}})); err != nil {
		t.Fatalf("daemon WriteJSON returned error: %v", err)
	}

	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	var snapshot protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &snapshot)
	if snapshot.Type != "daemon_snapshot" || len(snapshot.Daemons) != 1 {
		t.Fatalf("snapshot = %#v, want one visible daemon", snapshot)
	}

	if err := appConn.WriteJSON(protocol.ConnectivityFrame{
		Type:      "relay_tunnel_request",
		RequestID: "request-1",
		AttemptID: "attempt-1",
		DaemonID:  "dev-1",
	}); err != nil {
		t.Fatalf("relay_tunnel_request WriteJSON returned error: %v", err)
	}
	var daemonReady protocol.ConnectivityFrame
	readConnectivityFrame(t, daemonConn, &daemonReady)
	if daemonReady.Type != "relay_tunnel_ready" || daemonReady.Actor != "daemon" || daemonReady.TunnelToken == "" {
		t.Fatalf("daemon ready = %#v, want daemon tunnel token", daemonReady)
	}
	var appReady protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &appReady)
	if appReady.Type != "relay_tunnel_ready" || appReady.Actor != "android" || appReady.TunnelToken == "" {
		t.Fatalf("app ready = %#v, want android tunnel token", appReady)
	}

	appTunnel := dialTunnelWS(t, wsBase+"/connectivity/tunnel/ws?token="+appReady.TunnelToken)
	defer appTunnel.Close()
	daemonTunnel := dialTunnelWS(t, wsBase+"/connectivity/tunnel/ws?token="+daemonReady.TunnelToken)
	defer daemonTunnel.Close()

	appPayload := []byte("encrypted-quic-packet-from-app")
	if err := appTunnel.WriteMessage(websocket.BinaryMessage, appPayload); err != nil {
		t.Fatalf("app tunnel WriteMessage returned error: %v", err)
	}
	messageType, payload := readTunnelMessage(t, daemonTunnel)
	if messageType != websocket.BinaryMessage || string(payload) != string(appPayload) {
		t.Fatalf("daemon tunnel message type=%d payload=%q, want app payload", messageType, payload)
	}

	daemonPayload := []byte("encrypted-quic-packet-from-daemon")
	if err := daemonTunnel.WriteMessage(websocket.BinaryMessage, daemonPayload); err != nil {
		t.Fatalf("daemon tunnel WriteMessage returned error: %v", err)
	}
	messageType, payload = readTunnelMessage(t, appTunnel)
	if messageType != websocket.BinaryMessage || string(payload) != string(daemonPayload) {
		t.Fatalf("app tunnel message type=%d payload=%q, want daemon payload", messageType, payload)
	}
}

func TestConnectivityRelayTunnelRejectsReusedToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	appFingerprint := strings.Repeat("a", 64)
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", appFingerprint)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	server := httptest.NewServer(env.handler(nil))
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DaemonPublicKey:   strings.Repeat("b", 64),
		DaemonFingerprint: strings.Repeat("c", 64),
	}, []protocol.ConnectivityTrustedAndroid{{Fingerprint: appFingerprint}})); err != nil {
		t.Fatalf("daemon WriteJSON returned error: %v", err)
	}
	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	var snapshot protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &snapshot)
	if err := appConn.WriteJSON(protocol.ConnectivityFrame{Type: "relay_tunnel_request", RequestID: "request-1", AttemptID: "attempt-1", DaemonID: "dev-1"}); err != nil {
		t.Fatalf("relay_tunnel_request WriteJSON returned error: %v", err)
	}
	var daemonReady protocol.ConnectivityFrame
	readConnectivityFrame(t, daemonConn, &daemonReady)
	var appReady protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &appReady)

	first := dialTunnelWS(t, wsBase+"/connectivity/tunnel/ws?token="+appReady.TunnelToken)
	defer first.Close()
	headers := http.Header{}
	second, resp, err := websocket.DefaultDialer.Dial(wsBase+"/connectivity/tunnel/ws?token="+appReady.TunnelToken, headers)
	if second != nil {
		_ = second.Close()
	}
	if err == nil {
		t.Fatal("second tunnel dial succeeded, want reused token rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("reused token status = %d, want 403", status)
	}
	_ = daemonReady
}

func TestConnectivityDaemonWebSocketClosesOnAgentTokenRevoke(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	appSession, err := env.appAuth.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   strings.Repeat("b", 64),
		DaemonFingerprint: strings.Repeat("c", 64),
	}, nil)); err != nil {
		t.Fatalf("daemon register WriteJSON returned error: %v", err)
	}
	if err := daemonConn.WriteJSON(protocol.ConnectivityFrame{Type: "pair_invitation_reserve", RequestID: "corr-revoke"}); err != nil {
		t.Fatalf("reserve WriteJSON returned error: %v", err)
	}
	var reserved protocol.ConnectivityFrame
	readConnectivityFrame(t, daemonConn, &reserved)
	if reserved.Type != "pair_invitation_reserved" {
		t.Fatalf("reserved = %#v, want pair_invitation_reserved", reserved)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+agentToken.Record.ID, nil)
	req.Header.Set("Authorization", bearerAuth(appSession.AccessToken))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", rec.Code)
	}
	assertConnectivityClosed(t, daemonConn)
}

func TestConnectivityDaemonWebSocketCannotRegisterAfterAgentTokenRevoke(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	appSession, err := env.appAuth.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	handler := env.handler(nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+agentToken.Record.ID, nil)
	req.Header.Set("Authorization", bearerAuth(appSession.AccessToken))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", rec.Code)
	}
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   strings.Repeat("b", 64),
		DaemonFingerprint: strings.Repeat("c", 64),
	}, nil)); err != nil {
		t.Fatalf("daemon register WriteJSON returned error: %v", err)
	}
	assertConnectivityClosed(t, daemonConn)
}

func TestConnectivityPairingCompletesThroughRelayWithGoClient(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	android, err := pairtest.NewAndroidClient("Pixel")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	appSession, err := env.appAuth.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", android.Fingerprint)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	agentToken := env.createAgentToken(t, user.ID, "Laptop")
	server := httptest.NewServer(env.handler(nil))
	defer server.Close()
	wsBase := "ws" + strings.TrimPrefix(server.URL, "http")

	paths := connectivityTestDaemonPaths(t)
	identity, err := daemon.ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}

	daemonConn := dialConnectivityWS(t, wsBase+"/connectivity/daemon/ws", agentToken.Plaintext)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-1",
		DisplayName:       "Laptop",
		DaemonPublicKey:   strings.ToLower(hexPublicKey(identity.PublicKey)),
		DaemonFingerprint: identity.Fingerprint,
	}, nil)); err != nil {
		t.Fatalf("daemon register WriteJSON returned error: %v", err)
	}

	const correlationID = "corr-e2e"
	if err := daemonConn.WriteJSON(protocol.ConnectivityFrame{Type: "pair_invitation_reserve", RequestID: correlationID}); err != nil {
		t.Fatalf("reserve WriteJSON returned error: %v", err)
	}
	var reserved protocol.ConnectivityFrame
	readConnectivityFrame(t, daemonConn, &reserved)
	if reserved.Type != "pair_invitation_reserved" || reserved.AccountID == "" {
		t.Fatalf("reserved = %#v, want account-bound reservation", reserved)
	}

	invitation, err := daemon.CreatePairInvitation(paths, daemon.PairInvitationOptions{
		BaseURL:        server.URL,
		DeviceID:       "dev-1",
		DisplayName:    "Laptop",
		AccountID:      reserved.AccountID,
		CorrelationID:  correlationID,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}

	appConn := dialConnectivityWS(t, wsBase+"/api/connectivity/app/ws", appSession.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	var snapshot protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &snapshot)
	if snapshot.Type != "daemon_snapshot" || len(snapshot.Daemons) != 0 {
		t.Fatalf("snapshot = %#v, want no visible daemons before pairing completion", snapshot)
	}

	response, sas, err := android.PairingResponse(connectivityPairingInvitation(invitation), reserved.AccountID)
	if err != nil {
		t.Fatalf("PairingResponse returned error: %v", err)
	}
	if err := appConn.WriteJSON(protocol.ConnectivityFrame{
		Type:      "pair_response_submit",
		RequestID: "submit-1",
		PairingResponse: &protocol.ConnectivityPairingResponse{
			AccountID:          response.AccountID,
			InvitationID:       response.InvitationID,
			CorrelationID:      response.CorrelationID,
			AndroidPublicKey:   response.AndroidPublicKey,
			AndroidFingerprint: strings.ToUpper(response.AndroidFingerprint),
			AndroidDisplayName: response.AndroidDisplayName,
			Signature:          response.Signature,
		},
	}); err != nil {
		t.Fatalf("pair_response_submit WriteJSON returned error: %v", err)
	}

	var forwarded protocol.ConnectivityFrame
	readConnectivityFrame(t, daemonConn, &forwarded)
	if forwarded.Type != "pair_response_forward" || forwarded.PairingResponse == nil {
		t.Fatalf("forwarded = %#v, want pair_response_forward", forwarded)
	}
	pending, err := daemon.StorePendingPairingResponse(paths, connectivitypairing.AndroidResponse{
		Version:            connectivitypairing.Version,
		AccountID:          forwarded.PairingResponse.AccountID,
		InvitationID:       forwarded.PairingResponse.InvitationID,
		CorrelationID:      forwarded.PairingResponse.CorrelationID,
		AndroidPublicKey:   forwarded.PairingResponse.AndroidPublicKey,
		AndroidFingerprint: forwarded.PairingResponse.AndroidFingerprint,
		AndroidDisplayName: forwarded.PairingResponse.AndroidDisplayName,
		Signature:          forwarded.PairingResponse.Signature,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("StorePendingPairingResponse returned error: %v", err)
	}
	if pending.SAS != sas || pending.AndroidFingerprint != android.Fingerprint {
		t.Fatalf("pending = %#v, want Android SAS %s", pending, sas)
	}
	completed, err := daemon.ConfirmPendingPairingResponse(paths, invitation.InvitationID, sas, time.Now().UTC())
	if err != nil {
		t.Fatalf("ConfirmPendingPairingResponse returned error: %v", err)
	}
	if err := daemonConn.WriteJSON(protocol.ConnectivityPairCompletedFrame(completed.Device.Fingerprint)); err != nil {
		t.Fatalf("pair_completed WriteJSON returned error: %v", err)
	}

	var visible protocol.ConnectivityFrame
	readConnectivityFrame(t, appConn, &visible)
	if visible.Type != "paired_device_visible" || visible.Daemon == nil || visible.Daemon.DeviceID != "dev-1" {
		t.Fatalf("visible = %#v, want dev-1 visible after pairing completion", visible)
	}
}

func dialConnectivityWS(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	headers := http.Header{"Authorization": {"Bearer " + token}}
	conn, resp, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial %s status %d error: %v", url, status, err)
	}
	return conn
}

func dialTunnelWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial tunnel %s status %d error: %v", url, status, err)
	}
	return conn
}

func readTunnelMessage(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage returned error: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	return messageType, payload
}

func readConnectivityFrame(t *testing.T, conn *websocket.Conn, out *protocol.ConnectivityFrame) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	if err := conn.ReadJSON(out); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
}

func assertConnectivityClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	var frame protocol.ConnectivityFrame
	err := conn.ReadJSON(&frame)
	if err == nil {
		t.Fatalf("ReadJSON returned frame %#v, want websocket close", frame)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("ReadJSON timed out waiting for websocket close")
	}
	_ = conn.SetReadDeadline(time.Time{})
}

func connectivityTestDaemonPaths(t *testing.T) daemon.Paths {
	t.Helper()
	root, err := os.MkdirTemp("", "connectivity-daemon-")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})
	return daemon.Paths{
		ConfigDir:                filepath.Join(root, "c"),
		ConfigFile:               filepath.Join(root, "c", "daemon.json"),
		StateDir:                 filepath.Join(root, "s"),
		RuntimeDir:               filepath.Join(root, "r"),
		SocketPath:               filepath.Join(root, "r", "d.sock"),
		TmuxSocketPath:           filepath.Join(root, "r", "tmux.sock"),
		PIDFile:                  filepath.Join(root, "s", "pid"),
		StatusFile:               filepath.Join(root, "s", "status.json"),
		DeviceFile:               filepath.Join(root, "s", "device.json"),
		ConnectivityIdentityFile: filepath.Join(root, "s", "connectivity_identity.json"),
		PairingStateFile:         filepath.Join(root, "s", "pairing_state.json"),
	}
}

func connectivityPairingInvitation(invitation daemon.PairInvitation) connectivitypairing.Invitation {
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

func hexPublicKey(publicKey []byte) string {
	const hextable = "0123456789abcdef"
	out := make([]byte, len(publicKey)*2)
	for i, b := range publicKey {
		out[i*2] = hextable[b>>4]
		out[i*2+1] = hextable[b&0x0f]
	}
	return string(out)
}
