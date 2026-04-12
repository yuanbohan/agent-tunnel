package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

func TestAttachWebSocketRejectsCrossOriginBrowserDial(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(issued.AccessToken))
	headers.Set("Origin", "https://evil.example")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want origin rejection")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAttachWebSocketForwardsSnapshotLiveBytesAndInputForOwner(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	attachConn := dialAttachClient(t, server.URL, issued.AccessToken, "sess-1")
	defer attachConn.Close()

	open := readAgentFrame(t, agentConn)
	if open.Type != "attach_open" || open.ClientID == "" {
		t.Fatalf("attach_open = %#v, want client id", open)
	}

	if err := agentConn.WriteJSON(protocol.AttachReadyFrame(open.ClientID, 120, 40)); err != nil {
		t.Fatalf("WriteJSON attach_ready returned error: %v", err)
	}
	packet, err := protocol.EncodeTerminalBytesPacket(open.ClientID, []byte("snapshot bytes"))
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket returned error: %v", err)
	}
	if err := agentConn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatalf("WriteMessage snapshot returned error: %v", err)
	}
	if err := agentConn.WriteJSON(protocol.SnapshotDoneFrame(open.ClientID)); err != nil {
		t.Fatalf("WriteJSON snapshot_done returned error: %v", err)
	}

	attached := readAttachControl(t, attachConn)
	if attached.Type != "attached" || attached.SessionID != "sess-1" || attached.Cols != 120 || attached.Rows != 40 {
		t.Fatalf("attached = %#v, want sess-1 120x40", attached)
	}
	if snapshot := string(readAttachBinary(t, attachConn)); snapshot != "snapshot bytes" {
		t.Fatalf("snapshot = %q, want snapshot bytes", snapshot)
	}
	if done := readAttachControl(t, attachConn); done.Type != "snapshot_done" {
		t.Fatalf("snapshot_done = %#v, want snapshot_done", done)
	}

	if err := attachConn.WriteJSON(protocol.EncodeClientInputText("hello", true)); err != nil {
		t.Fatalf("WriteJSON input_text returned error: %v", err)
	}
	inputText := readAgentFrame(t, agentConn)
	if inputText.Type != "input_text" || inputText.ClientID != open.ClientID || inputText.Text != "hello" || !inputText.Submit {
		t.Fatalf("input_text = %#v, want routed submit hello", inputText)
	}

	if err := attachConn.WriteJSON(protocol.EncodeClientInputKey("TAB")); err != nil {
		t.Fatalf("WriteJSON input_key returned error: %v", err)
	}
	inputKey := readAgentFrame(t, agentConn)
	if inputKey.Type != "input_key" || inputKey.ClientID != open.ClientID || inputKey.Key != "TAB" {
		t.Fatalf("input_key = %#v, want routed TAB", inputKey)
	}

	if err := agentConn.WriteJSON(protocol.ResizeFrame(100, 30)); err != nil {
		t.Fatalf("WriteJSON resize returned error: %v", err)
	}
	if resize := readAttachControl(t, attachConn); resize.Type != "resize" || resize.Cols != 100 || resize.Rows != 30 {
		t.Fatalf("resize = %#v, want 100x30", resize)
	}
}

func TestLogoutClosesOnlyCurrentAppSessionAttachAndKeepsAgentSessionAlive(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	firstIssued := env.login(t, "alice", "password123")
	secondIssued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	attachConn := dialAttachClient(t, server.URL, firstIssued.AccessToken, "sess-1")
	defer attachConn.Close()

	open := readAgentFrame(t, agentConn)
	if err := agentConn.WriteJSON(protocol.AttachReadyFrame(open.ClientID, 120, 40)); err != nil {
		t.Fatalf("WriteJSON attach_ready returned error: %v", err)
	}
	if err := agentConn.WriteJSON(protocol.SnapshotDoneFrame(open.ClientID)); err != nil {
		t.Fatalf("WriteJSON snapshot_done returned error: %v", err)
	}
	if attached := readAttachControl(t, attachConn); attached.Type != "attached" {
		t.Fatalf("attached = %#v, want attached", attached)
	}
	if done := readAttachControl(t, attachConn); done.Type != "snapshot_done" {
		t.Fatalf("snapshot_done = %#v, want snapshot_done", done)
	}

	logoutResp := doBearerPOST(t, server.URL+"/api/auth/logout", firstIssued.AccessToken, "")
	defer logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.StatusCode)
	}

	if closing := readAttachControl(t, attachConn); closing.Type != "closing" || closing.Reason != "logged_out" {
		t.Fatalf("closing = %#v, want closing logged_out", closing)
	}
	if closeFrame := readAgentFrame(t, agentConn); closeFrame.Type != "attach_close" || closeFrame.ClientID != open.ClientID || closeFrame.Reason != "logged_out" {
		t.Fatalf("attach_close = %#v, want attach_close logged_out", closeFrame)
	}

	sessionsResp := doBearerGET(t, server.URL+"/api/sessions", secondIssued.AccessToken)
	defer sessionsResp.Body.Close()
	if sessionsResp.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", sessionsResp.StatusCode)
	}
	var sessions []protocol.SessionInfo
	if err := json.NewDecoder(sessionsResp.Body).Decode(&sessions); err != nil {
		t.Fatalf("Decode sessions returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %#v, want sess-1 still online", sessions)
	}
}

func TestPasswordChangeClosesUserAttachesAndKeepsAgentSessionAlive(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	attachConn := dialAttachClient(t, server.URL, issued.AccessToken, "sess-1")
	defer attachConn.Close()

	open := readAgentFrame(t, agentConn)
	if err := agentConn.WriteJSON(protocol.AttachReadyFrame(open.ClientID, 120, 40)); err != nil {
		t.Fatalf("WriteJSON attach_ready returned error: %v", err)
	}
	if err := agentConn.WriteJSON(protocol.SnapshotDoneFrame(open.ClientID)); err != nil {
		t.Fatalf("WriteJSON snapshot_done returned error: %v", err)
	}
	if attached := readAttachControl(t, attachConn); attached.Type != "attached" {
		t.Fatalf("attached = %#v, want attached", attached)
	}
	if done := readAttachControl(t, attachConn); done.Type != "snapshot_done" {
		t.Fatalf("snapshot_done = %#v, want snapshot_done", done)
	}

	changeResp := doBearerPOST(t, server.URL+"/api/auth/password/change", issued.AccessToken, `{"current_password":"password123","new_password":"betterpass456"}`)
	defer changeResp.Body.Close()
	if changeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change status = %d, want 204", changeResp.StatusCode)
	}

	if closing := readAttachControl(t, attachConn); closing.Type != "closing" || closing.Reason != "password_changed" {
		t.Fatalf("closing = %#v, want closing password_changed", closing)
	}
	if closeFrame := readAgentFrame(t, agentConn); closeFrame.Type != "attach_close" || closeFrame.ClientID != open.ClientID || closeFrame.Reason != "password_changed" {
		t.Fatalf("attach_close = %#v, want attach_close password_changed", closeFrame)
	}

	reloginResp := doJSONPOST(t, server.URL+"/api/auth/login", `{"username":"alice","password":"betterpass456"}`)
	defer reloginResp.Body.Close()
	if reloginResp.StatusCode != http.StatusOK {
		t.Fatalf("relogin status = %d, want 200", reloginResp.StatusCode)
	}
	var relogin appSessionResponse
	if err := json.NewDecoder(reloginResp.Body).Decode(&relogin); err != nil {
		t.Fatalf("Decode relogin returned error: %v", err)
	}

	sessionsResp := doBearerGET(t, server.URL+"/api/sessions", relogin.AccessToken)
	defer sessionsResp.Body.Close()
	if sessionsResp.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", sessionsResp.StatusCode)
	}
	var sessions []protocol.SessionInfo
	if err := json.NewDecoder(sessionsResp.Body).Decode(&sessions); err != nil {
		t.Fatalf("Decode sessions returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %#v, want sess-1 still online", sessions)
	}
}

func TestAttachWebSocketReturnsNotFoundForCrossUserAttach(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	alice := env.registerUser(t, "alice", "password123", "AB2C3D")
	bob := env.registerUser(t, "bob1", "password123", "EF4G5H")
	aliceIssued := env.login(t, "alice", "password123")
	bobIssued := env.login(t, "bob1", "password123")
	bobToken := env.createAgentToken(t, bob.ID, "Bob Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, bobToken.Plaintext, "sess-b")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-b", bob.ID)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-b/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(aliceIssued.AccessToken))

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want not found")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	_ = agentConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	var frame protocol.AgentFrame
	if err := agentConn.ReadJSON(&frame); err == nil {
		t.Fatalf("agent unexpectedly received frame %#v for cross-user attach", frame)
	}
	if alice.ID == 0 || bobIssued.AccessToken == "" {
		t.Fatal("expected valid users for cross-user test setup")
	}
}

func TestAgentWebSocketRejectsUnknownAgentToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth("does-not-exist"))

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want unauthorized")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAgentRegistrationKeepsLiveSessionsUserScopedAcrossOwners(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	alice := env.registerUser(t, "alice", "password123", "AB2C3D")
	bob := env.registerUser(t, "bob1", "password123", "EF4G5H")
	aliceIssued := env.login(t, "alice", "password123")
	bobIssued := env.login(t, "bob1", "password123")
	aliceToken := env.createAgentToken(t, alice.ID, "Alice Laptop")
	bobToken := env.createAgentToken(t, bob.ID, "Bob Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	aliceAgentConn := dialAndRegisterAgent(t, server.URL, aliceToken.Plaintext, "sess-a")
	defer aliceAgentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-a", alice.ID)
	bobAgentConn := dialAndRegisterAgent(t, server.URL, bobToken.Plaintext, "sess-b")
	defer bobAgentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-b", bob.ID)

	aliceResp := doBearerGET(t, server.URL+"/api/sessions", aliceIssued.AccessToken)
	defer aliceResp.Body.Close()
	if aliceResp.StatusCode != http.StatusOK {
		t.Fatalf("alice status = %d, want 200", aliceResp.StatusCode)
	}
	var aliceSessions []protocol.SessionInfo
	if err := json.NewDecoder(aliceResp.Body).Decode(&aliceSessions); err != nil {
		t.Fatalf("Decode alice sessions returned error: %v", err)
	}
	if len(aliceSessions) != 1 || aliceSessions[0].SessionID != "sess-a" {
		t.Fatalf("alice sessions = %#v, want only sess-a", aliceSessions)
	}

	bobResp := doBearerGET(t, server.URL+"/api/sessions", bobIssued.AccessToken)
	defer bobResp.Body.Close()
	if bobResp.StatusCode != http.StatusOK {
		t.Fatalf("bob status = %d, want 200", bobResp.StatusCode)
	}
	var bobSessions []protocol.SessionInfo
	if err := json.NewDecoder(bobResp.Body).Decode(&bobSessions); err != nil {
		t.Fatalf("Decode bob sessions returned error: %v", err)
	}
	if len(bobSessions) != 1 || bobSessions[0].SessionID != "sess-b" {
		t.Fatalf("bob sessions = %#v, want only sess-b", bobSessions)
	}
}
