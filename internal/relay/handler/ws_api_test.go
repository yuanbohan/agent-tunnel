package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	handlerresponse "yuanbohan/tunnel/internal/relay/handler/response"
	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

func TestDeviceWebSocketRegistersAndListsDeviceForOwner(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	deviceConn := dialAndRegisterDevice(t, server.URL, agentToken.Plaintext, protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Yuanbo's MacBook Pro",
		PlatformFamily: "macos",
		PlatformID:     "macos",
		LaunchHealth:   "healthy",
	})
	defer deviceConn.Close()

	resp := doBearerGET(t, server.URL+"/api/devices", issued.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var devices []protocol.DeviceInfo
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &devices)
	if len(devices) != 1 || devices[0].DeviceID != "dev-1" || devices[0].LaunchHealth != "healthy" {
		t.Fatalf("devices = %#v, want registered device", devices)
	}
}

func TestDeviceWebSocketUpdateChangesListedLaunchHealthForOwner(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	deviceConn := dialAndRegisterDevice(t, server.URL, agentToken.Plaintext, protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Yuanbo's MacBook Pro",
		PlatformFamily: "macos",
		PlatformID:     "macos",
		LaunchHealth:   "healthy",
	})
	defer deviceConn.Close()

	if err := deviceConn.WriteJSON(protocol.DeviceUpdateFrame(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Yuanbo's MacBook Pro",
		PlatformFamily: "macos",
		PlatformID:     "macos",
		LaunchHealth:   "degraded",
	})); err != nil {
		t.Fatalf("WriteJSON update returned error: %v", err)
	}

	resp := doBearerGET(t, server.URL+"/api/devices", issued.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var devices []protocol.DeviceInfo
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &devices)
	if len(devices) != 1 || devices[0].DeviceID != "dev-1" || devices[0].LaunchHealth != "degraded" {
		t.Fatalf("devices = %#v, want updated degraded launch health", devices)
	}
}

func TestDeviceWebSocketRejectsEmptyDeviceID(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	deviceConn := dialAndRegisterDevice(t, server.URL, agentToken.Plaintext, protocol.DeviceInfo{
		DeviceID:       "   ",
		DisplayName:    "Broken Device",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	})
	defer deviceConn.Close()

	resp := doBearerGET(t, server.URL+"/api/devices", issued.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var devices []protocol.DeviceInfo
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &devices)
	if len(devices) != 0 {
		t.Fatalf("devices = %#v, want no registered devices for empty device_id", devices)
	}
}

func TestDeviceWebSocketLaunchRequestRoundTrip(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	deviceConn := dialAndRegisterDevice(t, server.URL, agentToken.Plaintext, protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Test Mac",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	})
	defer deviceConn.Close()

	done := make(chan struct{})
	requestIDCh := make(chan string, 1)
	agentConnCh := make(chan *websocket.Conn, 1)
	go func() {
		defer close(done)
		var frame protocol.DeviceFrame
		if err := deviceConn.ReadJSON(&frame); err != nil {
			t.Errorf("ReadJSON returned error: %v", err)
			return
		}
		requestIDCh <- frame.RequestID
		if frame.Type != "launch_request" || frame.Command != "codex" || frame.CWD != "/repo" || frame.Label != "api-fix" {
			t.Errorf("frame = %#v, want launch_request codex /repo api-fix", frame)
			return
		}
		if err := deviceConn.WriteJSON(protocol.DeviceLaunchResultFrame(frame.RequestID, "accepted", "")); err != nil {
			t.Errorf("WriteJSON returned error: %v", err)
			return
		}

		agentConnCh <- dialAndRegisterAgentWithLaunchRequest(t, server.URL, agentToken.Plaintext, "sess-1", frame.RequestID)
	}()

	resp := doBearerPOST(t, server.URL+"/api/devices/dev-1/launch", issued.AccessToken, `{"command":"codex","cwd":"/repo","label":"api-fix"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var launch handlertypes.DeviceLaunchResponse
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &launch)
	requestID := <-requestIDCh
	if launch.Status != "session_ready" || launch.SessionID != "sess-1" || launch.Reason != "" || launch.RequestID != requestID {
		t.Fatalf("launch = %#v, want session_ready sess-1", launch)
	}
	agentConn := <-agentConnCh
	if err := agentConn.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	<-done
}

func TestDeviceWebSocketPendingPeerCannotRegisterAfterTokenRevoke(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/device/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(agentToken.Plaintext))
	deviceConn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial device websocket returned error: %v", err)
	}
	defer deviceConn.Close()

	revokeResp := doBearerDELETE(t, server.URL+"/api/agent-tokens/"+agentToken.Record.ID, issued.AccessToken)
	defer revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", revokeResp.StatusCode)
	}

	err = deviceConn.WriteJSON(protocol.DeviceRegisterFrame(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Revoked Device",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	}))
	if err == nil {
		_ = deviceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err = deviceConn.ReadMessage()
	}
	if err == nil {
		t.Fatal("device websocket stayed usable after token revoke")
	}

	resp := doBearerGET(t, server.URL+"/api/devices", issued.AccessToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var devices []protocol.DeviceInfo
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &devices)
	if len(devices) != 0 {
		t.Fatalf("devices = %#v, want no devices after revoke", devices)
	}
}

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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	decodeAPIErrorEnvelopeFromResponse(t, resp, http.StatusForbidden, handlerresponse.CodeForbidden, "The request is forbidden.")
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
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutResp.StatusCode)
	}
	decodeAPIEnvelopeFromResponse(t, logoutResp, http.StatusOK, nil)

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
	decodeAPIEnvelopeFromResponse(t, sessionsResp, http.StatusOK, &sessions)
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
	if changeResp.StatusCode != http.StatusOK {
		t.Fatalf("password change status = %d, want 200", changeResp.StatusCode)
	}
	decodeAPIEnvelopeFromResponse(t, changeResp, http.StatusOK, nil)

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
	decodeAPIEnvelopeFromResponse(t, reloginResp, http.StatusOK, &relogin)

	sessionsResp := doBearerGET(t, server.URL+"/api/sessions", relogin.AccessToken)
	defer sessionsResp.Body.Close()
	if sessionsResp.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", sessionsResp.StatusCode)
	}
	var sessions []protocol.SessionInfo
	decodeAPIEnvelopeFromResponse(t, sessionsResp, http.StatusOK, &sessions)
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	decodeAPIErrorEnvelopeFromResponse(t, resp, http.StatusNotFound, handlerresponse.CodeSessionNotFound, "The session was not found or is offline.")

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
	decodeAPIEnvelopeFromResponse(t, aliceResp, http.StatusOK, &aliceSessions)
	if len(aliceSessions) != 1 || aliceSessions[0].SessionID != "sess-a" {
		t.Fatalf("alice sessions = %#v, want only sess-a", aliceSessions)
	}

	bobResp := doBearerGET(t, server.URL+"/api/sessions", bobIssued.AccessToken)
	defer bobResp.Body.Close()
	if bobResp.StatusCode != http.StatusOK {
		t.Fatalf("bob status = %d, want 200", bobResp.StatusCode)
	}
	var bobSessions []protocol.SessionInfo
	decodeAPIEnvelopeFromResponse(t, bobResp, http.StatusOK, &bobSessions)
	if len(bobSessions) != 1 || bobSessions[0].SessionID != "sess-b" {
		t.Fatalf("bob sessions = %#v, want only sess-b", bobSessions)
	}
}
