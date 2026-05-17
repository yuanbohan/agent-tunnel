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

func TestDeviceWebSocketRegistersAndListsComputerForOwner(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := doBearerGET(t, server.URL+"/api/computers", issued.AccessToken)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var computers []handlertypes.ComputerInfo
		decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &computers)
		resp.Body.Close()
		if len(computers) == 1 && computers[0].ComputerID == "dev-1" && computers[0].LaunchHealth == "healthy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("computers = %#v, want registered computer", computers)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDeviceWebSocketUpdateChangesListedComputerLaunchHealthForOwner(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := doBearerGET(t, server.URL+"/api/computers", issued.AccessToken)
		var computers []handlertypes.ComputerInfo
		decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &computers)
		resp.Body.Close()
		if len(computers) == 1 && computers[0].ComputerID == "dev-1" && computers[0].LaunchHealth == "degraded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("computers = %#v, want updated degraded launch health", computers)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDeviceWebSocketLaunchRequestRoundTripEndsAtSessionReady(t *testing.T) {
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
		if err := deviceConn.WriteJSON(protocol.DeviceLaunchResultFrameWithWorkspace(frame.RequestID, "accepted", "", "launch_fixed")); err != nil {
			t.Errorf("WriteJSON returned error: %v", err)
			return
		}

		agentConn := dialAndRegisterAgentWithLaunchRequestAndDeviceID(t, server.URL, agentToken.Plaintext, "sess-1", frame.RequestID, "dev-1")
		if err := agentConn.WriteJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: frame.RequestID})); err != nil {
			t.Errorf("WriteJSON launch_ready returned error: %v", err)
			return
		}
		agentConnCh <- agentConn
	}()

	resp := doBearerPOST(t, server.URL+"/api/computers/dev-1/sessions", issued.AccessToken, `{"command":"codex","cwd":"/repo","label":"api-fix"}`)
	defer resp.Body.Close()
	var launch handlertypes.DeviceLaunchResponse
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &launch)
	requestID := <-requestIDCh
	if launch.Status != "session_ready" || launch.SessionID != "sess-1" || launch.Reason != "" || launch.RequestID != requestID {
		t.Fatalf("launch = %#v, want session_ready sess-1", launch)
	}
	agentConn := <-agentConnCh
	defer agentConn.Close()

	sessionsResp := doBearerGET(t, server.URL+"/api/sessions", issued.AccessToken)
	defer sessionsResp.Body.Close()
	decodeAPIErrorEnvelopeFromResponse(t, sessionsResp, http.StatusNotFound, handlerresponse.CodeNotFound, "The requested endpoint was not found.")
	<-done
}

func TestDeviceWebSocketLaunchWaitsForAgentLaunchReady(t *testing.T) {
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

	launchRespCh := make(chan handlertypes.DeviceLaunchResponse, 1)
	go func() {
		resp := doBearerPOST(t, server.URL+"/api/computers/dev-1/sessions", issued.AccessToken, `{"command":"codex","cwd":"/repo"}`)
		defer resp.Body.Close()
		var launch handlertypes.DeviceLaunchResponse
		decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &launch)
		launchRespCh <- launch
	}()

	var frame protocol.DeviceFrame
	if err := deviceConn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON launch_request returned error: %v", err)
	}
	if err := deviceConn.WriteJSON(protocol.DeviceLaunchResultFrameWithWorkspace(frame.RequestID, "accepted", "", "launch_fixed")); err != nil {
		t.Fatalf("WriteJSON accepted returned error: %v", err)
	}

	agentConn := dialAndRegisterAgentWithLaunchRequestAndDeviceID(t, server.URL, agentToken.Plaintext, "sess-1", frame.RequestID, "dev-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	select {
	case launch := <-launchRespCh:
		t.Fatalf("launch completed before launch_ready: %#v", launch)
	case <-time.After(100 * time.Millisecond):
	}

	if err := agentConn.WriteJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: frame.RequestID})); err != nil {
		t.Fatalf("WriteJSON launch_ready returned error: %v", err)
	}

	select {
	case launch := <-launchRespCh:
		if launch.Status != "session_ready" || launch.SessionID != "sess-1" || launch.RequestID != frame.RequestID {
			t.Fatalf("launch = %#v, want session_ready sess-1", launch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for launch response after launch_ready")
	}
}

func TestDeviceWebSocketLateAcceptedLaunchBackfillsMobileSourceInRegistry(t *testing.T) {
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

	launchRespCh := make(chan handlertypes.DeviceLaunchResponse, 1)
	go func() {
		resp := doBearerPOST(t, server.URL+"/api/computers/dev-1/sessions", issued.AccessToken, `{"command":"codex","cwd":"/repo"}`)
		defer resp.Body.Close()
		var launch handlertypes.DeviceLaunchResponse
		decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &launch)
		launchRespCh <- launch
	}()

	var frame protocol.DeviceFrame
	if err := deviceConn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON launch_request returned error: %v", err)
	}

	agentConn := dialAndRegisterAgentWithLaunchRequestAndDeviceID(t, server.URL, agentToken.Plaintext, "sess-1", frame.RequestID, "dev-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)
	if err := agentConn.WriteJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: frame.RequestID})); err != nil {
		t.Fatalf("WriteJSON launch_ready returned error: %v", err)
	}

	if !env.registry.HasSession("sess-1") {
		t.Fatal("registry missing live session before accepted result")
	}

	if err := deviceConn.WriteJSON(protocol.DeviceLaunchResultFrameWithWorkspace(frame.RequestID, "accepted", "", "launch_fixed")); err != nil {
		t.Fatalf("WriteJSON accepted returned error: %v", err)
	}

	select {
	case launch := <-launchRespCh:
		if launch.Status != "session_ready" || launch.SessionID != "sess-1" || launch.RequestID != frame.RequestID {
			t.Fatalf("launch = %#v, want session_ready sess-1", launch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for launch response")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if env.registry.HasSession("sess-1") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for live session after late accepted result")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentRegistrationWithoutLaunchPreservesDeviceIDButForcesLocalLaunchSource(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgentWithLaunchRequestAndDeviceID(t, server.URL, agentToken.Plaintext, "sess-direct", "", "dev-existing")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-direct", user.ID)

	if !env.registry.HasSession("sess-direct") {
		t.Fatal("registered agent session missing")
	}
}

func TestAttachWebSocketRouteIsRemoved(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(issued.AccessToken))

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want removed route")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	defer resp.Body.Close()
	decodeAPIErrorEnvelopeFromResponse(t, resp, http.StatusNotFound, handlerresponse.CodeNotFound, "The requested endpoint was not found.")
}

func TestLogoutRevokesAppSessionAndKeepsAgentSessionAlive(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	firstIssued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-1")
	defer agentConn.Close()
	waitForOwnedSession(t, env.registry, "sess-1", user.ID)

	logoutResp := doBearerPOST(t, server.URL+"/api/auth/logout", firstIssued.AccessToken, "")
	defer logoutResp.Body.Close()
	decodeAPIEnvelopeFromResponse(t, logoutResp, http.StatusOK, nil)

	if !env.registry.HasSession("sess-1") {
		t.Fatal("agent session missing after app logout")
	}
}

func TestPasswordChangeRevokesAppSessionsAndKeepsAgentSessionAlive(t *testing.T) {
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

	changeResp := doBearerPOST(t, server.URL+"/api/auth/password/change", issued.AccessToken, `{"current_password":"password123","new_password":"betterpass456"}`)
	defer changeResp.Body.Close()
	decodeAPIEnvelopeFromResponse(t, changeResp, http.StatusOK, nil)

	if !env.registry.HasSession("sess-1") {
		t.Fatal("agent session missing after password change")
	}

	policyResp := doBearerGET(t, server.URL+"/api/account/policy", issued.AccessToken)
	defer policyResp.Body.Close()
	decodeAPIErrorEnvelopeFromResponse(t, policyResp, http.StatusUnauthorized, handlerresponse.CodeUnauthorized, "The request is unauthorized.")
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
	decodeAPIEnvelopeFromResponse(t, revokeResp, http.StatusOK, nil)

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

	resp := doBearerGET(t, server.URL+"/api/computers", issued.AccessToken)
	defer resp.Body.Close()
	var computers []handlertypes.ComputerInfo
	decodeAPIEnvelopeFromResponse(t, resp, http.StatusOK, &computers)
	if len(computers) != 0 {
		t.Fatalf("computers = %#v, want no computers after revoke", computers)
	}
}

func TestAgentWebSocketCannotRegisterAfterTokenRevoke(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	agentToken := env.createAgentToken(t, user.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(agentToken.Plaintext))
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial agent websocket returned error: %v", err)
	}
	defer agentConn.Close()

	revokeResp := doBearerDELETE(t, server.URL+"/api/agent-tokens/"+agentToken.Record.ID, issued.AccessToken)
	defer revokeResp.Body.Close()
	decodeAPIEnvelopeFromResponse(t, revokeResp, http.StatusOK, nil)

	err = agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: "sess-revoked",
		Launcher:  "codex",
		CWD:       "/repo",
	}))
	if err == nil {
		_ = agentConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err = agentConn.ReadMessage()
	}
	if err == nil {
		t.Fatal("agent websocket stayed usable after token revoke")
	}
	if env.registry.HasSession("sess-revoked") {
		t.Fatal("revoked token registered a live agent session")
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
