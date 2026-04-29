package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

func TestConnectivityPairingCoreFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), scenarioTimeout)
	defer cancel()

	h := newHarness(t)
	h.Prepare(ctx)

	client := newAppClient(h.baseURL)
	runID := time.Now().UTC().UnixNano()
	username := fmt.Sprintf("connectivity%d", runID)
	password := "password123"

	inviteCode := h.CreateInvite(ctx)
	if _, err := client.Register(inviteCode, username, password); err != nil {
		t.Fatalf("register returned error: %v", err)
	}

	android, err := pairtest.NewAndroidClient("Pixel E2E")
	if err != nil {
		t.Fatalf("NewAndroidClient returned error: %v", err)
	}
	issued, err := client.LoginWithDeviceFingerprint(username, password, android.Fingerprint)
	if err != nil {
		t.Fatalf("LoginWithDeviceFingerprint returned error: %v", err)
	}

	policy, err := client.AccountPolicy(issued.AccessToken)
	if err != nil {
		t.Fatalf("AccountPolicy returned error: %v", err)
	}
	if policy.Tier != "free" {
		t.Fatalf("initial policy tier = %q, want free", policy.Tier)
	}
	setUserTier(t, ctx, h, username, "pro")
	policy, err = client.AccountPolicy(issued.AccessToken)
	if err != nil {
		t.Fatalf("AccountPolicy after tier update returned error: %v", err)
	}
	if policy.Tier != "pro" {
		t.Fatalf("updated policy tier = %q, want pro", policy.Tier)
	}

	createdToken, err := client.CreateAgentToken(issued.AccessToken, "Connectivity E2E")
	if err != nil {
		t.Fatalf("CreateAgentToken returned error: %v", err)
	}

	daemonPaths := connectivityE2EDaemonPaths(t)
	identity, err := daemon.ReadOrCreateConnectivityIdentity(daemonPaths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}

	daemonConn := dialE2EConnectivityWS(t, h.baseURL, "/connectivity/daemon/ws", createdToken.Token)
	defer daemonConn.Close()
	if err := daemonConn.WriteJSON(protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          "dev-connectivity-e2e",
		DisplayName:       "Connectivity E2E Mac",
		PlatformFamily:    "test",
		PlatformID:        "test",
		DaemonPublicKey:   fmt.Sprintf("%x", identity.PublicKey),
		DaemonFingerprint: identity.Fingerprint,
	}, nil)); err != nil {
		t.Fatalf("daemon register WriteJSON returned error: %v", err)
	}

	const correlationID = "corr-connectivity-e2e"
	if err := daemonConn.WriteJSON(protocol.ConnectivityFrame{Type: "pair_invitation_reserve", RequestID: correlationID}); err != nil {
		t.Fatalf("pair_invitation_reserve WriteJSON returned error: %v", err)
	}
	reserved := readE2EConnectivityFrame(t, daemonConn)
	assertConnectivityFrameType(t, reserved, "pair_invitation_reserved")
	if reserved.AccountID == "" || reserved.AccountID != issued.AccountID {
		t.Fatalf("reserved account = %q, want app account %q", reserved.AccountID, issued.AccountID)
	}

	invitation, err := daemon.CreatePairInvitation(daemonPaths, daemon.PairInvitationOptions{
		BaseURL:        h.baseURL,
		DeviceID:       "dev-connectivity-e2e",
		DisplayName:    "Connectivity E2E Mac",
		AccountID:      reserved.AccountID,
		CorrelationID:  correlationID,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}

	appConn := dialE2EConnectivityWS(t, h.baseURL, "/api/connectivity/app/ws", issued.AccessToken)
	defer appConn.Close()
	if err := appConn.WriteJSON(protocol.ConnectivityAppRegisterFrame()); err != nil {
		t.Fatalf("app register WriteJSON returned error: %v", err)
	}
	snapshot := readE2EConnectivityFrame(t, appConn)
	assertConnectivityFrameType(t, snapshot, "daemon_snapshot")
	if len(snapshot.Daemons) != 0 {
		t.Fatalf("snapshot = %#v, want no visible daemons before pairing", snapshot)
	}

	response, sas, err := android.PairingResponse(connectivityE2EInvitation(invitation), reserved.AccountID)
	if err != nil {
		t.Fatalf("PairingResponse returned error: %v", err)
	}
	if err := appConn.WriteJSON(protocol.ConnectivityFrame{
		Type:      "pair_response_submit",
		RequestID: "submit-connectivity-e2e",
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

	forwarded := readE2EConnectivityFrame(t, daemonConn)
	assertConnectivityFrameType(t, forwarded, "pair_response_forward")
	if forwarded.PairingResponse == nil || strings.ToLower(forwarded.PairingResponse.AndroidFingerprint) != android.Fingerprint {
		t.Fatalf("forwarded = %#v, want Android pairing response", forwarded)
	}
	pending, err := daemon.StorePendingPairingResponse(daemonPaths, connectivitypairing.AndroidResponse{
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
	if pending.SAS != sas {
		t.Fatalf("pending SAS = %q, want %q", pending.SAS, sas)
	}
	completed, err := daemon.ConfirmPendingPairingResponse(daemonPaths, invitation.InvitationID, sas, time.Now().UTC())
	if err != nil {
		t.Fatalf("ConfirmPendingPairingResponse returned error: %v", err)
	}
	if err := daemonConn.WriteJSON(protocol.ConnectivityPairCompletedFrame(completed.Device.Fingerprint)); err != nil {
		t.Fatalf("pair_completed WriteJSON returned error: %v", err)
	}

	visible := readE2EConnectivityFrame(t, appConn)
	assertConnectivityFrameType(t, visible, "paired_device_visible")
	if visible.Daemon == nil || visible.Daemon.DeviceID != "dev-connectivity-e2e" {
		t.Fatalf("visible = %#v, want paired daemon", visible)
	}

	revoked, err := daemon.RevokeTrustedAndroidDevice(daemonPaths, android.Fingerprint)
	if err != nil {
		t.Fatalf("RevokeTrustedAndroidDevice returned error: %v", err)
	}
	if err := daemonConn.WriteJSON(protocol.ConnectivityFrame{
		Type:               "paired_device_revoked",
		AndroidFingerprint: revoked.Fingerprint,
	}); err != nil {
		t.Fatalf("paired_device_revoked WriteJSON returned error: %v", err)
	}
	revokedFrame := readE2EConnectivityFrame(t, appConn)
	assertConnectivityFrameType(t, revokedFrame, "paired_device_revoked")
	if revokedFrame.Daemon == nil || revokedFrame.Daemon.DeviceID != "dev-connectivity-e2e" {
		t.Fatalf("revoked frame = %#v, want paired daemon revoke", revokedFrame)
	}
}

func setUserTier(t *testing.T, ctx context.Context, h *Harness, username, tier string) {
	t.Helper()
	cmd := exec.CommandContext(h.commandContext(ctx), h.binaries.relay, "user", "tier", username, tier)
	cmd.Env = h.commandEnv(map[string]string{
		"RELAY_LISTEN_ADDR":    h.listenAddr,
		"RELAY_OPERATOR_TOKEN": h.operatorToken,
	})
	if output, err := runCommand("relay user tier", cmd); err != nil {
		t.Fatalf("relay user tier returned error: %v\n%s", err, output)
	}
}

func dialE2EConnectivityWS(t *testing.T, baseURL, path, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(strings.TrimRight(baseURL, "/"), "http") + path
	headers := http.Header{"Authorization": {"Bearer " + token}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial %s status %d error: %v", wsURL, status, err)
	}
	return conn
}

func readE2EConnectivityFrame(t *testing.T, conn *websocket.Conn) protocol.ConnectivityFrame {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	var frame protocol.ConnectivityFrame
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	return frame
}

func assertConnectivityFrameType(t *testing.T, frame protocol.ConnectivityFrame, want string) {
	t.Helper()
	if frame.Type != want {
		t.Fatalf("frame type = %q frame=%#v, want %q", frame.Type, frame, want)
	}
	switch frame.Type {
	case "daemon_snapshot", "pair_invitation_reserved", "pair_response_forward", "paired_device_visible", "paired_device_revoked":
	default:
		t.Fatalf("unexpected Step 2 connectivity frame type %q", frame.Type)
	}
}

func connectivityE2EDaemonPaths(t *testing.T) daemon.Paths {
	t.Helper()
	root, err := os.MkdirTemp("", "connectivity-e2e-daemon-")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})
	return daemon.Paths{
		ConfigDir:                filepath.Join(root, "config"),
		ConfigFile:               filepath.Join(root, "config", "daemon.json"),
		StateDir:                 filepath.Join(root, "state"),
		RuntimeDir:               filepath.Join(root, "runtime"),
		SocketPath:               filepath.Join(root, "runtime", "daemon.sock"),
		TmuxSocketPath:           filepath.Join(root, "runtime", "tmux.sock"),
		PIDFile:                  filepath.Join(root, "state", "daemon.pid"),
		StatusFile:               filepath.Join(root, "state", "status.json"),
		DeviceFile:               filepath.Join(root, "state", "device.json"),
		ConnectivityIdentityFile: filepath.Join(root, "state", "connectivity_identity.json"),
		PairingStateFile:         filepath.Join(root, "state", "pairing_state.json"),
	}
}

func connectivityE2EInvitation(invitation daemon.PairInvitation) connectivitypairing.Invitation {
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
