package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

const scenarioTimeout = 2 * time.Minute

func TestLocalRegressionFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), scenarioTimeout)
	defer cancel()

	h := newHarness(t)
	h.Prepare(ctx)

	client := newAppClient(h.baseURL)
	runID := time.Now().UTC().UnixNano()
	username := fmt.Sprintf("e2euser%d", runID)
	password := "password123"
	newPassword := "betterpass456"

	inviteCode := h.CreateInvite(ctx)
	register, err := client.Register(inviteCode, username, password)
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}
	if register.Username != username {
		t.Fatalf("register username = %q, want %q", register.Username, username)
	}

	issued, err := client.Login(username, password)
	if err != nil {
		t.Fatalf("login returned error: %v", err)
	}

	createdToken, err := client.CreateAgentToken(issued.AccessToken, "Local E2E")
	if err != nil {
		t.Fatalf("CreateAgentToken returned error: %v", err)
	}

	tunnel := h.StartTunnel(createdToken.Token)
	waitForTunnelOutput(t, ctx, tunnel, "READY e2e-launcher")

	session := waitForSingleSession(t, ctx, client, issued.AccessToken, tunnel)
	attachConn, err := client.Attach(issued.AccessToken, session.SessionID)
	if err != nil {
		t.Fatalf("Attach returned error: %v", err)
	}
	defer attachConn.Close()

	attached, snapshot, err := readSnapshot(t, attachConn, 5*time.Second)
	if err != nil {
		t.Fatalf("readSnapshot returned error: %v", err)
	}
	if attached.Type != "attached" || attached.SessionID != session.SessionID {
		t.Fatalf("attached = %#v, want attached for %s", attached, session.SessionID)
	}
	if attached.Cols != 100 || attached.Rows != 30 {
		t.Fatalf("attached size = %dx%d, want 100x30", attached.Cols, attached.Rows)
	}
	if !bytes.Contains(snapshot, []byte("READY e2e-launcher")) {
		t.Fatalf("snapshot = %q, want ready banner", snapshot)
	}

	if err := attachConn.WriteJSON(protocol.EncodeClientInputText("ping", true)); err != nil {
		t.Fatalf("WriteJSON input_text returned error: %v", err)
	}
	liveBytes, err := readBinaryUntilContains(attachConn, "REPLY ping", 5*time.Second)
	if err != nil {
		t.Fatalf("readBinaryUntilContains returned error: %v", err)
	}
	if !bytes.Contains(liveBytes, []byte("REPLY ping")) {
		t.Fatalf("live bytes = %q, want REPLY ping", liveBytes)
	}

	if err := client.ChangePassword(issued.AccessToken, password, newPassword); err != nil {
		t.Fatalf("ChangePassword returned error: %v", err)
	}

	closing, err := readControl(attachConn, 5*time.Second)
	if err != nil {
		t.Fatalf("readControl returned error: %v", err)
	}
	if closing.Type != "closing" || closing.Reason != "password_changed" {
		t.Fatalf("closing = %#v, want password_changed", closing)
	}
	assertAttachClosed(t, attachConn, 2*time.Second)

	status, body, err := client.GetSessionsStatus(issued.AccessToken)
	if err != nil {
		t.Fatalf("GetSessionsStatus returned error: %v", err)
	}
	if status != 401 {
		t.Fatalf("old access token status = %d body=%q, want 401", status, body)
	}

	relogin, err := client.Login(username, newPassword)
	if err != nil {
		t.Fatalf("relogin returned error: %v", err)
	}

	reattachedSession := waitForSingleSession(t, ctx, client, relogin.AccessToken, tunnel)
	if reattachedSession.SessionID != session.SessionID {
		t.Fatalf("session after relogin = %q, want %q", reattachedSession.SessionID, session.SessionID)
	}

	reattachConn, err := client.Attach(relogin.AccessToken, session.SessionID)
	if err != nil {
		t.Fatalf("re-attach returned error: %v", err)
	}
	defer reattachConn.Close()

	reattached, secondSnapshot, err := readSnapshot(t, reattachConn, 5*time.Second)
	if err != nil {
		t.Fatalf("second readSnapshot returned error: %v", err)
	}
	if reattached.Type != "attached" || reattached.SessionID != session.SessionID {
		t.Fatalf("reattached = %#v, want attached for %s", reattached, session.SessionID)
	}
	if !bytes.Contains(secondSnapshot, []byte("REPLY ping")) {
		t.Fatalf("second snapshot = %q, want prior command output", secondSnapshot)
	}

	assertDurableState(t, ctx, h, username, register.UserID)
}

func assertDurableState(t *testing.T, ctx context.Context, h *Harness, username string, userID int64) {
	t.Helper()

	user, err := loadUserByUsername(ctx, h.db, username)
	if err != nil {
		t.Fatalf("loadUserByUsername returned error: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("user.ID = %d, want %d", user.ID, userID)
	}
	if user.UsernameNorm != username {
		t.Fatalf("user.UsernameNorm = %q, want %q", user.UsernameNorm, username)
	}

	invite, err := loadInviteForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadInviteForUser returned error: %v", err)
	}
	if !invite.ConsumedAt.Valid {
		t.Fatal("invite was not marked consumed")
	}
	if !invite.ConsumedByUserID.Valid || invite.ConsumedByUserID.Int64 != user.ID {
		t.Fatalf("invite consumed_by_user_id = %#v, want %d", invite.ConsumedByUserID, user.ID)
	}

	appSessions, err := loadAppSessionsForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadAppSessionsForUser returned error: %v", err)
	}
	if len(appSessions) != 2 {
		t.Fatalf("len(appSessions) = %d, want 2", len(appSessions))
	}
	if !appSessions[0].RevokedAt.Valid || appSessions[0].RevokeReason != "password_changed" {
		t.Fatalf("first app session = %#v, want revoked password_changed", appSessions[0])
	}
	if appSessions[1].RevokedAt.Valid {
		t.Fatalf("second app session = %#v, want active session", appSessions[1])
	}

	agentTokens, err := loadAgentTokensForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadAgentTokensForUser returned error: %v", err)
	}
	if len(agentTokens) != 1 {
		t.Fatalf("len(agentTokens) = %d, want 1", len(agentTokens))
	}
	if agentTokens[0].Name != "Local E2E" {
		t.Fatalf("agent token name = %q, want Local E2E", agentTokens[0].Name)
	}
	if !agentTokens[0].LastUsedAt.Valid {
		t.Fatalf("agent token = %#v, want last_used_at set", agentTokens[0])
	}
	if agentTokens[0].RevokedAt.Valid {
		t.Fatalf("agent token = %#v, want active token", agentTokens[0])
	}
}

func waitForSingleSession(t *testing.T, ctx context.Context, client *AppClient, accessToken string, tunnel *TunnelProcess) protocol.SessionInfo {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tunnel.Exited() {
			t.Fatalf("tunnel exited before session discovery: %v\n%s", tunnel.WaitErr(), tunnel.Tail())
		}
		sessions, err := client.ListSessions(accessToken)
		if err == nil && len(sessions) == 1 {
			return sessions[0]
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled while waiting for session: %v", ctx.Err())
		case <-time.After(150 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for session discovery\n%s", tunnel.Tail())
	return protocol.SessionInfo{}
}

func waitForTunnelOutput(t *testing.T, ctx context.Context, tunnel *TunnelProcess, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tunnel.Exited() {
			t.Fatalf("tunnel exited before output %q appeared: %v\n%s", want, tunnel.WaitErr(), tunnel.Tail())
		}
		if strings.Contains(tunnel.Tail(), want) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled while waiting for tunnel output %q: %v", want, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for tunnel output %q\n%s", want, tunnel.Tail())
}

func readSnapshot(t *testing.T, conn *websocket.Conn, timeout time.Duration) (protocol.AttachControlMessage, []byte, error) {
	t.Helper()

	var attached protocol.AttachControlMessage
	var snapshot bytes.Buffer
	deadline := time.Now().Add(timeout)

	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return attached, nil, err
		}
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return attached, nil, err
		}

		switch messageType {
		case websocket.TextMessage:
			var control protocol.AttachControlMessage
			if err := json.Unmarshal(payload, &control); err != nil {
				return attached, nil, err
			}
			if control.Type == "attached" {
				attached = control
				continue
			}
			if control.Type == "snapshot_done" {
				return attached, snapshot.Bytes(), nil
			}
		case websocket.BinaryMessage:
			snapshot.Write(payload)
		}
	}
}

func readBinaryUntilContains(conn *websocket.Conn, want string, timeout time.Duration) ([]byte, error) {
	var out bytes.Buffer
	deadline := time.Now().Add(timeout)

	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		switch messageType {
		case websocket.BinaryMessage:
			out.Write(payload)
			if strings.Contains(out.String(), want) {
				return out.Bytes(), nil
			}
		case websocket.TextMessage:
			var control protocol.AttachControlMessage
			if err := json.Unmarshal(payload, &control); err != nil {
				return nil, err
			}
			if control.Type == "closing" {
				return nil, fmt.Errorf("attach closed before seeing %q: %s", want, control.Reason)
			}
		}
	}
}

func readControl(conn *websocket.Conn, timeout time.Duration) (protocol.AttachControlMessage, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return protocol.AttachControlMessage{}, err
		}
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return protocol.AttachControlMessage{}, err
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var control protocol.AttachControlMessage
		if err := json.Unmarshal(payload, &control); err != nil {
			return protocol.AttachControlMessage{}, err
		}
		return control, nil
	}
}

func assertAttachClosed(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("attach websocket remained open after closing control")
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("attach websocket did not close within %s: %v", timeout, err)
	}
}
