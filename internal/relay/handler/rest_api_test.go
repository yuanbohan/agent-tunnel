package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
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
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("login response unmarshal error: %v", err)
	}
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
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("refresh response unmarshal error: %v", err)
	}
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
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutRec.Code)
	}

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
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("refresh at 29 days unmarshal error: %v", err)
	}
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(29 * 24 * time.Hour)
	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh at 58 days status = %d, want 200", refreshRec.Code)
	}
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("refresh at 58 days unmarshal error: %v", err)
	}
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(29 * 24 * time.Hour)
	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh at 87 days status = %d, want 200", refreshRec.Code)
	}
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("refresh at 87 days unmarshal error: %v", err)
	}
	currentRefreshToken = refreshed.RefreshToken

	env.now = env.now.Add(3*24*time.Hour - time.Hour)

	refreshRec = httptest.NewRecorder()
	refreshReq = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+currentRefreshToken+`"}`))
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh near absolute expiry status = %d, want 200", refreshRec.Code)
	}

	var refreshResp appSessionResponse
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("refresh response unmarshal error: %v", err)
	}
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
	if changeRec.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d, want 204", changeRec.Code)
	}

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
		SessionID: "sess-a",
		Launcher:  "codex",
		StartedAt: 20,
	}, SessionOwner{UserID: alice.ID, AgentTokenID: "agt-a"}, fakeAgentPeer{})
	env.registry.RegisterOwned(protocol.SessionInfo{
		SessionID: "sess-b",
		Launcher:  "codex",
		StartedAt: 10,
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
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-a" {
		t.Fatalf("sessions = %#v, want only sess-a", sessions)
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
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create response unmarshal error: %v", err)
	}
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
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list response unmarshal error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %#v, want created token metadata", listed)
	}

	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created.ID, nil)
	deleteReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", deleteRec.Code)
	}

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

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if sessions := env.registry.ListForUser(user.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty after token revoke", sessions)
	}
	if reasons := attachPeer.CloseReasons(); len(reasons) != 1 || reasons[0] != "agent_token_revoked" {
		t.Fatalf("close reasons = %#v, want [agent_token_revoked]", reasons)
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

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
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
