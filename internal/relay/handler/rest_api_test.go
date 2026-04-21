package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/protocol"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
	relaydevice "yuanbohan/tunnel/internal/relay/device"
	handlerresponse "yuanbohan/tunnel/internal/relay/handler/response"
	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

func TestHandlerRejectsSessionsWithoutBearerAuth(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="tunnel relay"` {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}
	decodeAPIErrorEnvelopeFromRecorder(t, rec, http.StatusUnauthorized, handlerresponse.CodeUnauthorized, "The request is unauthorized.")
}

func TestHandlerListsDevicesForAuthenticatedUser(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")

	env.deviceRegistry.RegisterOwned(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Yuanbo's MacBook Pro",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	}, relaydevice.DeviceOwner{UserID: user.ID}, &fakeDevicePeerForHandler{})
	env.deviceRegistry.RegisterOwned(protocol.DeviceInfo{
		DeviceID:       "dev-2",
		DisplayName:    "Ubuntu Box",
		PlatformFamily: "linux",
		PlatformID:     "ubuntu",
	}, relaydevice.DeviceOwner{UserID: user.ID + 1}, &fakeDevicePeerForHandler{})

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var devices []protocol.DeviceInfo
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, &devices)
	if len(devices) != 1 || devices[0].DeviceID != "dev-1" {
		t.Fatalf("devices = %#v, want only dev-1", devices)
	}
}

func TestHandlerLaunchDeviceWaitsForSessionReady(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	peer := &blockingDevicePeer{sent: make(chan protocol.DeviceFrame, 1)}
	env.deviceRegistry.RegisterOwned(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Test Mac",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	}, relaydevice.DeviceOwner{UserID: user.ID}, peer)

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	requestIDCh := make(chan string, 1)
	go func() {
		frame := <-peer.sent
		requestIDCh <- frame.RequestID
		env.deviceRegistry.ResolveLaunchIfOwner("dev-1", peer, frame.RequestID, relaydevice.LaunchStatusAccepted, "")
		env.deviceRegistry.CompleteLaunchIfOwner(frame.RequestID, relaydevice.DeviceOwner{UserID: user.ID}, "sess-1")
		close(done)
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/launch", strings.NewReader(`{"command":"claude","cwd":"/repo","label":"api-fix"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response handlertypes.DeviceLaunchResponse
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, &response)
	requestID := <-requestIDCh
	if response.Status != relaydevice.LaunchStatusSessionReady || response.SessionID != "sess-1" || response.RequestID != requestID || response.Reason != "" {
		t.Fatalf("response = %#v, want session_ready sess-1", response)
	}
	<-done
}

func TestHandlerRevokingAgentTokenCompletesInFlightLaunch(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	created := env.createAgentToken(t, user.ID, "Laptop")
	peer := &blockingDevicePeer{sent: make(chan protocol.DeviceFrame, 1)}
	env.deviceRegistry.RegisterOwned(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Test Mac",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	}, relaydevice.DeviceOwner{UserID: user.ID, AgentTokenID: created.Record.ID}, peer)

	handler := env.handler(nil)
	launchRec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/launch", strings.NewReader(`{"command":"claude","cwd":"/repo"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
		handler.ServeHTTP(launchRec, req)
	}()

	select {
	case <-peer.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("launch request was not forwarded before revoke")
	}

	revokeRec := httptest.NewRecorder()
	revokeReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created.Record.ID, nil)
	revokeReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", revokeRec.Code)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("launch request did not resolve after token revoke")
	}

	var response handlertypes.DeviceLaunchResponse
	decodeAPIEnvelopeFromRecorder(t, launchRec, http.StatusOK, &response)
	if response.Status != relaydevice.LaunchStatusFailed || response.Reason != "device_offline" || response.RequestID == "" {
		t.Fatalf("response = %#v, want device_offline", response)
	}
}

func TestHandlerNoRouteReturnsEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	handler.ServeHTTP(rec, req)

	decodeAPIErrorEnvelopeFromRecorder(t, rec, http.StatusNotFound, handlerresponse.CodeNotFound, "The requested endpoint was not found.")
}

func TestHandlerNoMethodReturnsEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	handler.ServeHTTP(rec, req)

	decodeAPIErrorEnvelopeFromRecorder(t, rec, http.StatusMethodNotAllowed, handlerresponse.CodeMethodNotAllowed, "The HTTP method is not allowed for this endpoint.")
}

func TestHandlerRecoveryReturnsEnvelope(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil).(*gin.Engine)
	handler.GET("/api/panic", func(c *gin.Context) {
		panic("boom")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/panic", nil)
	handler.ServeHTTP(rec, req)

	decodeAPIErrorEnvelopeFromRecorder(t, rec, http.StatusInternalServerError, handlerresponse.CodeInternalError, "An unexpected internal error occurred.")
}

func TestHandlerRegisterLoginRefreshLogoutFlow(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	handler := env.handler(nil)

	registerRec := httptest.NewRecorder()
	registerReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"invite_code":"ab2c3d","username":"Alice","password":"password123"}`))
	handler.ServeHTTP(registerRec, registerReq)
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", registerRec.Code)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"alice","password":"password123"}`))
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginRec.Code)
	}

	var loginResp appSessionResponse
	decodeAPIEnvelopeFromRecorder(t, loginRec, http.StatusOK, &loginResp)
	if loginResp.AccessToken == "" || loginResp.RefreshToken == "" {
		t.Fatalf("login response = %#v, want tokens", loginResp)
	}
	if loginResp.ExpiresIn != int64(relayauth.DefaultAccessTokenTTL/time.Second) {
		t.Fatalf("login expires_in = %d, want %d", loginResp.ExpiresIn, int64(relayauth.DefaultAccessTokenTTL/time.Second))
	}

	refreshRec := httptest.NewRecorder()
	refreshReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+loginResp.RefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", refreshRec.Code)
	}

	var refreshResp appSessionResponse
	decodeAPIEnvelopeFromRecorder(t, refreshRec, http.StatusOK, &refreshResp)
	if refreshResp.AccessToken == loginResp.AccessToken || refreshResp.RefreshToken == loginResp.RefreshToken {
		t.Fatalf("refresh response = %#v, want rotated tokens", refreshResp)
	}
	if refreshResp.ExpiresIn != int64(relayauth.DefaultAccessTokenTTL/time.Second) {
		t.Fatalf("refresh expires_in = %d, want %d", refreshResp.ExpiresIn, int64(relayauth.DefaultAccessTokenTTL/time.Second))
	}

	logoutRec := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.Header.Set("Authorization", bearerAuth(refreshResp.AccessToken))
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutRec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, logoutRec, http.StatusOK, nil)

	refreshAfterLogoutRec := httptest.NewRecorder()
	refreshAfterLogoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refreshResp.RefreshToken+`"}`))
	handler.ServeHTTP(refreshAfterLogoutRec, refreshAfterLogoutReq)
	if refreshAfterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status = %d, want 401", refreshAfterLogoutRec.Code)
	}
}

func TestHandlerRefreshClampsExpiresInAtAbsoluteSessionBoundary(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	currentRefreshToken := issued.RefreshToken
	handler := env.handler(nil)

	env.now = env.now.Add(29 * 24 * time.Hour)
	refreshRec := httptest.NewRecorder()
	refreshReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh at 29 days status = %d, want 200", refreshRec.Code)
	}
	var refreshed appSessionResponse
	decodeAPIEnvelopeFromRecorder(t, refreshRec, http.StatusOK, &refreshed)
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(29 * 24 * time.Hour)
	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh at 58 days status = %d, want 200", refreshRec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, refreshRec, http.StatusOK, &refreshed)
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(29 * 24 * time.Hour)
	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh at 87 days status = %d, want 200", refreshRec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, refreshRec, http.StatusOK, &refreshed)
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(3*24*time.Hour - time.Hour)

	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh near absolute expiry status = %d, want 200", refreshRec.Code)
	}

	var refreshResp appSessionResponse
	decodeAPIEnvelopeFromRecorder(t, refreshRec, http.StatusOK, &refreshResp)
	if refreshResp.ExpiresIn != int64(time.Hour/time.Second) {
		t.Fatalf("refresh expires_in near absolute expiry = %d, want %d", refreshResp.ExpiresIn, int64(time.Hour/time.Second))
	}

	env.now = env.now.Add(time.Hour)

	sessionsAtBoundaryRec := httptest.NewRecorder()
	sessionsAtBoundaryReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	sessionsAtBoundaryReq.Header.Set("Authorization", bearerAuth(refreshResp.AccessToken))
	handler.ServeHTTP(sessionsAtBoundaryRec, sessionsAtBoundaryReq)
	if sessionsAtBoundaryRec.Code != http.StatusUnauthorized {
		t.Fatalf("sessions at absolute expiry status = %d, want 401", sessionsAtBoundaryRec.Code)
	}

	refreshAtBoundaryRec := httptest.NewRecorder()
	refreshAtBoundaryReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refreshResp.RefreshToken+`"}`))
	handler.ServeHTTP(refreshAtBoundaryRec, refreshAtBoundaryReq)
	if refreshAtBoundaryRec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh at absolute expiry status = %d, want 401", refreshAtBoundaryRec.Code)
	}

	env.now = env.now.Add(time.Minute)

	sessionsRec := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	sessionsReq.Header.Set("Authorization", bearerAuth(refreshResp.AccessToken))
	handler.ServeHTTP(sessionsRec, sessionsReq)
	if sessionsRec.Code != http.StatusUnauthorized {
		t.Fatalf("sessions after absolute expiry status = %d, want 401", sessionsRec.Code)
	}

	refreshExpiredRec := httptest.NewRecorder()
	refreshExpiredReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refreshResp.RefreshToken+`"}`))
	handler.ServeHTTP(refreshExpiredRec, refreshExpiredReq)
	if refreshExpiredRec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after absolute expiry status = %d, want 401", refreshExpiredRec.Code)
	}
}

func TestHandlerPasswordChangeRevokesCurrentSession(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	handler := env.handler(nil)

	changeRec := httptest.NewRecorder()
	changeReq := httptest.NewRequest(http.MethodPost, "/api/auth/password/change", strings.NewReader(`{"current_password":"password123","new_password":"betterpass456"}`))
	changeReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(changeRec, changeReq)
	if changeRec.Code != http.StatusOK {
		t.Fatalf("password change status = %d, want 200", changeRec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, changeRec, http.StatusOK, nil)

	authorizedRec := httptest.NewRecorder()
	authorizedReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	authorizedReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(authorizedRec, authorizedReq)
	if authorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("sessions with old access token status = %d, want 401", authorizedRec.Code)
	}

	oldLoginRec := httptest.NewRecorder()
	oldLoginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"alice","password":"password123"}`))
	handler.ServeHTTP(oldLoginRec, oldLoginReq)
	if oldLoginRec.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d, want 401", oldLoginRec.Code)
	}

	newLoginRec := httptest.NewRecorder()
	newLoginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"alice","password":"betterpass456"}`))
	handler.ServeHTTP(newLoginRec, newLoginReq)
	if newLoginRec.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, want 200", newLoginRec.Code)
	}
}

func TestHandlerRegisterThrottleByIP(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.throttle = NewRegisterThrottle(2, 10*time.Minute)
	env.throttle.SetNowFunc(func() time.Time { return env.now })
	handler := env.handler(nil)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"invite_code":"ZZZZZZ","username":"alice","password":"password123"}`))
		req.RemoteAddr = "198.51.100.10:1234"
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d status = %d, want 400", i+1, rec.Code)
		}
	}

	limitedRec := httptest.NewRecorder()
	limitedReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"invite_code":"ZZZZZZ","username":"alice","password":"password123"}`))
	limitedReq.RemoteAddr = "198.51.100.10:1234"
	handler.ServeHTTP(limitedRec, limitedReq)
	if limitedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d, want 429", limitedRec.Code)
	}
	if limitedRec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing on throttled response")
	}

	otherIPRec := httptest.NewRecorder()
	otherIPReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"invite_code":"ZZZZZZ","username":"alice","password":"password123"}`))
	otherIPReq.RemoteAddr = "198.51.100.11:1234"
	handler.ServeHTTP(otherIPRec, otherIPReq)
	if otherIPRec.Code != http.StatusBadRequest {
		t.Fatalf("other IP status = %d, want 400", otherIPRec.Code)
	}
}

func TestHandlerReturnsUserScopedLiveSessions(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	alice := env.registerUser(t, "alice", "password123", "AB2C3D")
	bob := env.registerUser(t, "bob1", "password123", "EF4G5H")
	aliceSession := env.login(t, "alice", "password123")

	env.registry.RegisterOwned(protocol.SessionInfo{
		SessionID:      "sess-a",
		DeviceID:       "dev-a",
		Launcher:       "codex",
		CommandPreview: "codex",
		GitBranch:      "main",
		StartedAt:      20,
		PlatformFamily: "linux",
		PlatformID:     "ubuntu",
		ComputerName:   "Office Linux",
	}, SessionOwner{UserID: alice.ID, AgentTokenID: "agt-a"}, fakeAgentPeer{})
	env.registry.RegisterOwned(protocol.SessionInfo{
		SessionID:      "sess-b",
		Launcher:       "codex",
		StartedAt:      10,
		PlatformFamily: "macos",
		PlatformID:     "macos",
		ComputerName:   "Bob Mac",
	}, SessionOwner{UserID: bob.ID, AgentTokenID: "agt-b"}, fakeAgentPeer{})

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", bearerAuth(aliceSession.AccessToken))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var sessions []protocol.SessionInfo
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, &sessions)
	if len(sessions) != 1 || sessions[0].SessionID != "sess-a" {
		t.Fatalf("sessions = %#v, want only sess-a", sessions)
	}
	if sessions[0].PlatformFamily != "linux" {
		t.Fatalf("PlatformFamily = %q, want linux", sessions[0].PlatformFamily)
	}
	if sessions[0].PlatformID != "ubuntu" {
		t.Fatalf("PlatformID = %q, want ubuntu", sessions[0].PlatformID)
	}
	if sessions[0].ComputerName != "Office Linux" {
		t.Fatalf("ComputerName = %q, want Office Linux", sessions[0].ComputerName)
	}
	if sessions[0].GitBranch != "main" {
		t.Fatalf("GitBranch = %q, want main", sessions[0].GitBranch)
	}
	if sessions[0].DeviceID != "dev-a" {
		t.Fatalf("DeviceID = %q, want dev-a", sessions[0].DeviceID)
	}
}

func TestHandlerAgentTokenEndpoints(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	handler := env.handler(nil)

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", strings.NewReader(`{"name":"MacBook"}`))
	createReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createRec.Code)
	}

	var created createdAgentTokenResponse
	decodeAPIEnvelopeFromRecorder(t, createRec, http.StatusCreated, &created)
	if created.Token == "" || created.Name != "MacBook" {
		t.Fatalf("create response = %#v, want token and name", created)
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/agent-tokens", nil)
	listReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRec.Code)
	}

	var listed []agentTokenResponse
	decodeAPIEnvelopeFromRecorder(t, listRec, http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %#v, want created token metadata", listed)
	}

	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created.ID, nil)
	deleteReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", deleteRec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, deleteRec, http.StatusOK, nil)

	record, err := env.store.AuthenticateAgentToken(context.Background(), env.digester.Digest(created.Token), env.now)
	if !errors.Is(err, ErrAgentTokenRevoked) {
		t.Fatalf("AuthenticateAgentToken error = %v, want ErrAgentTokenRevoked", err)
	}
	if record != (AgentTokenRecord{}) {
		t.Fatalf("record = %#v, want zero record on revoked auth", record)
	}
	if user.ID == 0 {
		t.Fatal("expected user id for revoke audit path")
	}
}

func TestHandlerAgentTokenDeleteDisconnectsLiveSessionImmediately(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	created := env.createAgentToken(t, user.ID, "Laptop")

	env.registry.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	}, SessionOwner{UserID: user.ID, AgentTokenID: created.Record.ID}, fakeAgentPeer{})
	attachPeer := &recordingAttachPeer{}
	if _, err := env.registry.StartAttachForUser("sess-1", "client-1", user.ID, attachPeer); err != nil {
		t.Fatalf("StartAttachForUser returned error: %v", err)
	}

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created.Record.ID, nil)
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, nil)
	if sessions := env.registry.ListForUser(user.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty after token revoke", sessions)
	}
	if reasons := attachPeer.CloseReasons(); len(reasons) != 1 || reasons[0] != "agent_token_revoked" {
		t.Fatalf("close reasons = %#v, want [agent_token_revoked]", reasons)
	}
}

func TestHandlerAgentTokenDeleteDisconnectsLiveDevicesImmediately(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	created := env.createAgentToken(t, user.ID, "Laptop")

	env.deviceRegistry.RegisterOwned(protocol.DeviceInfo{
		DeviceID:       "dev-1",
		DisplayName:    "Laptop",
		PlatformFamily: "macos",
		PlatformID:     "macos",
	}, relaydevice.DeviceOwner{UserID: user.ID, AgentTokenID: created.Record.ID}, &fakeDevicePeerForHandler{})

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created.Record.ID, nil)
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, nil)
	if devices := env.deviceRegistry.ListForUser(user.ID); len(devices) != 0 {
		t.Fatalf("devices = %#v, want empty after token revoke", devices)
	}
}

func TestHandlerOperatorDeleteUserDisconnectsLiveSessionImmediately(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")

	env.registry.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	}, SessionOwner{UserID: user.ID, AgentTokenID: "agt-1"}, fakeAgentPeer{})
	attachPeer := &recordingAttachPeer{}
	if _, err := env.registry.StartAttachForUser("sess-1", "client-1", user.ID, attachPeer); err != nil {
		t.Fatalf("StartAttachForUser returned error: %v", err)
	}

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, OperatorDeleteUserPath, strings.NewReader(`{"username":"alice"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", bearerAuth(env.operatorTok))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, nil)
	if sessions := env.registry.ListForUser(user.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty after user delete", sessions)
	}
	if reasons := attachPeer.CloseReasons(); len(reasons) != 1 || reasons[0] != "account_deleted" {
		t.Fatalf("close reasons = %#v, want [account_deleted]", reasons)
	}
}

func TestHandlerOperatorRoutesRequireBearerToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, OperatorInviteCodesPath, strings.NewReader(`{"count":1,"expires_in_days":7}`))
	req.RemoteAddr = "127.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="tunnel relay"` {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}
}

func TestHandlerOperatorRoutesRejectNonLoopbackRequests(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, OperatorInviteCodesPath, strings.NewReader(`{"count":1,"expires_in_days":7}`))
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("Authorization", bearerAuth(env.operatorTok))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandlerOperatorRoutesRejectForwardedRequests(t *testing.T) {
	env := newHandlerTestEnv(t)
	handler := env.handler(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, OperatorInviteCodesPath, strings.NewReader(`{"count":1,"expires_in_days":7}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("Authorization", bearerAuth(env.operatorTok))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandlerOperatorListInvites(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	env.registerUser(t, "alice", "password123", "AB2C3D")

	handler := env.handler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, OperatorInviteListPath, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", bearerAuth(env.operatorTok))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var payload handlertypes.OperatorInviteCodesListResponse
	decodeAPIEnvelopeFromRecorder(t, rec, http.StatusOK, &payload)
	if len(payload.Invites) != 2 {
		t.Fatalf("len(invites) = %d, want 2", len(payload.Invites))
	}
	var consumedByAlice bool
	for _, invite := range payload.Invites {
		if invite.Code == "AB2C3D" && invite.Consumed {
			consumedByAlice = invite.ConsumedByUsername == "alice"
		}
	}
	if !consumedByAlice {
		t.Fatalf("invites = %#v, want AB2C3D consumed by alice", payload.Invites)
	}
}
