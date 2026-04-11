package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

type mockWSConn struct {
	mu             sync.Mutex
	deadline       time.Time
	messages       [][]byte
	setDeadlineErr error
	writeErr       error
	closeErr       error
}

func (m *mockWSConn) WriteMessage(_ int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	m.messages = append(m.messages, append([]byte(nil), data...))
	return nil
}

func (m *mockWSConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setDeadlineErr != nil {
		return m.setDeadlineErr
	}
	m.deadline = t
	return nil
}

func (m *mockWSConn) Close() error { return m.closeErr }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type handlerTestEnv struct {
	now         time.Time
	store       *fakeStore
	digester    *SecretDigester
	appAuth     *AppAuthService
	agentTokens *AgentTokenService
	operator    *OperatorService
	operatorTok string
	throttle    *RegisterThrottle
	registry    *Registry
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()

	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	store := newFakeStore(func() time.Time { return now })
	digester, err := NewSecretDigester("test-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	hasher := PasswordHasher{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}
	appAuth := NewAppAuthService(store, digester, hasher)
	appAuth.now = func() time.Time { return now }

	agentTokens := NewAgentTokenService(store, digester)
	agentTokens.now = func() time.Time { return now }

	operator := NewOperatorService(store, digester)
	operator.now = func() time.Time { return now }

	throttle := NewRegisterThrottle(5, 10*time.Minute)
	throttle.now = func() time.Time { return now }

	return &handlerTestEnv{
		now:         now,
		store:       store,
		digester:    digester,
		appAuth:     appAuth,
		agentTokens: agentTokens,
		operator:    operator,
		operatorTok: "operator-secret",
		throttle:    throttle,
		registry:    NewRegistry(),
	}
}

func (e *handlerTestEnv) handler(logger *Logger) http.Handler {
	return NewHandler(HandlerConfig{
		Registry:         e.registry,
		AppAuth:          e.appAuth,
		AgentTokens:      e.agentTokens,
		Operator:         e.operator,
		OperatorToken:    e.operatorTok,
		RegisterThrottle: e.throttle,
		Logger:           logger,
	})
}

func (e *handlerTestEnv) addInvite(t *testing.T, code string) {
	t.Helper()
	if _, err := e.store.CreateInviteCode(context.Background(), CreateInviteCodeParams{
		CodeDigest: e.digester.Digest(code),
		CodeHint:   code[len(code)-2:],
		CreatedBy:  "tester",
		ExpiresAt:  e.now.Add(24 * time.Hour),
		Now:        e.now,
	}); err != nil {
		t.Fatalf("CreateInviteCode returned error: %v", err)
	}
}

func (e *handlerTestEnv) registerUser(t *testing.T, username, password, inviteCode string) User {
	t.Helper()
	user, err := e.appAuth.Register(context.Background(), username, password, inviteCode)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	return user
}

func (e *handlerTestEnv) login(t *testing.T, username, password string) IssuedAppSession {
	t.Helper()
	issued, err := e.appAuth.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	return issued
}

func (e *handlerTestEnv) createAgentToken(t *testing.T, userID int64, name string) CreatedAgentToken {
	t.Helper()
	created, err := e.agentTokens.Create(context.Background(), userID, name)
	if err != nil {
		t.Fatalf("Create agent token returned error: %v", err)
	}
	return created
}

func readLogEntries(t *testing.T, buf *syncBuffer) []map[string]any {
	t.Helper()

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}

	lines := strings.Split(raw, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Unmarshal log line returned error: %v\nline: %s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func countLogEvents(entries []map[string]any, event string) int {
	count := 0
	for _, entry := range entries {
		if logString(entry, "event") == event {
			count++
		}
	}
	return count
}

func findLogEntryByEventAndPath(t *testing.T, entries []map[string]any, event, path string) map[string]any {
	t.Helper()
	for _, entry := range entries {
		if logString(entry, "event") == event && logString(entry, "path") == path {
			return entry
		}
	}
	t.Fatalf("missing log entry event=%q path=%q in %#v", event, path, entries)
	return nil
}

func findLogEntryByEvent(t *testing.T, entries []map[string]any, event string) map[string]any {
	t.Helper()
	for _, entry := range entries {
		if logString(entry, "event") == event {
			return entry
		}
	}
	t.Fatalf("missing log entry event=%q in %#v", event, entries)
	return nil
}

func logString(entry map[string]any, key string) string {
	value, _ := entry[key].(string)
	return value
}

func logNumber(t *testing.T, entry map[string]any, key string) float64 {
	t.Helper()
	value, ok := entry[key].(float64)
	if !ok {
		t.Fatalf("entry[%q] = %#v, want number in %#v", key, entry[key], entry)
	}
	return value
}

func TestWSAgentPeerSendJSONSetsWriteDeadline(t *testing.T) {
	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
		active:       true,
	}

	if err := peer.SendJSON(protocol.ResizeFrame(120, 40)); err != nil {
		t.Fatalf("SendJSON returned error: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.deadline.IsZero() {
		t.Fatal("SetWriteDeadline was not called")
	}
	if len(conn.messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(conn.messages))
	}
}

func TestWSAgentPeerSendJSONReturnsDeadlineError(t *testing.T) {
	conn := &mockWSConn{setDeadlineErr: errors.New("deadline failed")}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
		active:       true,
	}

	if err := peer.SendJSON(protocol.ResizeFrame(120, 40)); err == nil || err.Error() != "deadline failed" {
		t.Fatalf("SendJSON error = %v, want deadline failed", err)
	}
}

func TestWSAgentPeerRejectsSendsAfterDeactivate(t *testing.T) {
	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
		active:       true,
	}

	peer.Deactivate()

	if err := peer.SendJSON(protocol.ResizeFrame(120, 40)); !errors.Is(err, errAgentPeerInactive) {
		t.Fatalf("SendJSON error = %v, want errAgentPeerInactive", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.messages) != 0 {
		t.Fatalf("message count = %d, want 0", len(conn.messages))
	}
}

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
	env.throttle.now = func() time.Time { return env.now }
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
	env.registerUser(t, "bob1", "password123", "EF4G5H")
	aliceIssued := env.login(t, "alice", "password123")
	bobIssued := env.login(t, "bob1", "password123")
	bobToken := env.createAgentToken(t, 2, "Bob Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, bobToken.Plaintext, "sess-b")
	defer agentConn.Close()

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

func TestAgentRegistrationMakesSessionVisibleOnlyToOwner(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.addInvite(t, "EF4G5H")
	alice := env.registerUser(t, "alice", "password123", "AB2C3D")
	env.registerUser(t, "bob1", "password123", "EF4G5H")
	aliceIssued := env.login(t, "alice", "password123")
	bobIssued := env.login(t, "bob1", "password123")
	agentToken := env.createAgentToken(t, alice.ID, "Laptop")

	server := httptest.NewServer(env.handler(nil))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, agentToken.Plaintext, "sess-a")
	defer agentConn.Close()

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
		t.Fatalf("alice sessions = %#v, want sess-a", aliceSessions)
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
	if len(bobSessions) != 0 {
		t.Fatalf("bob sessions = %#v, want none", bobSessions)
	}
}

func TestHandlerAccessLogsRequestsAndSkipsHealthz(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	env.registry.RegisterOwned(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, SessionOwner{UserID: 1, AgentTokenID: "agt-1"}, fakeAgentPeer{})
	logs := &syncBuffer{}

	handler := env.handler(NewLogger(logs))

	healthRec := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(healthRec, healthReq)

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	unauthReq.Header.Set("User-Agent", "scanbot/1.0")
	unauthReq.Header.Set("X-Request-Id", "req-unauth-1")
	handler.ServeHTTP(unauthRec, unauthReq)

	badMethodRec := httptest.NewRecorder()
	badMethodReq := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/attach/ws", nil)
	badMethodReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(badMethodRec, badMethodReq)

	entries := readLogEntries(t, logs)
	if countLogEvents(entries, "http_request_completed") != 2 {
		t.Fatalf("http_request_completed count = %d, want 2", countLogEvents(entries, "http_request_completed"))
	}

	for _, entry := range entries {
		if logString(entry, "event") == "http_request_completed" && logString(entry, "path") == "/healthz" {
			t.Fatalf("unexpected healthz access log: %#v", entry)
		}
	}

	unauthEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions")
	if got := int(logNumber(t, unauthEntry, "status")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
	if got := logString(unauthEntry, "target"); got != "/api/sessions" {
		t.Fatalf("target = %q, want /api/sessions", got)
	}
	if got := logString(unauthEntry, "user_agent"); got != "scanbot/1.0" {
		t.Fatalf("user_agent = %q, want scanbot/1.0", got)
	}
	if got := logString(unauthEntry, "request_id"); got != "req-unauth-1" {
		t.Fatalf("request_id = %q, want req-unauth-1", got)
	}
	if got := int64(logNumber(t, unauthEntry, "response_bytes")); got != int64(unauthRec.Body.Len()) {
		t.Fatalf("response_bytes = %d, want %d", got, unauthRec.Body.Len())
	}

	badMethodEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/attach/ws")
	if got := int(logNumber(t, badMethodEntry, "status")); got != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", got)
	}

	authFailed := findLogEntryByEvent(t, entries, "auth_failed")
	if got := logString(authFailed, "path"); got != "/api/sessions" {
		t.Fatalf("auth_failed path = %q, want /api/sessions", got)
	}
}

func TestHandlerLogsWebSocketUpgradeFailureWithoutLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	user := env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	env.registry.RegisterOwned(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, SessionOwner{UserID: user.ID, AgentTokenID: "agt-1"}, fakeAgentPeer{})
	logs := &syncBuffer{}
	handler := env.handler(NewLogger(logs))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/attach/ws", nil)
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	entries := readLogEntries(t, logs)
	upgradeFailed := findLogEntryByEvent(t, entries, "ws_upgrade_failed")
	if got := logString(upgradeFailed, "path"); got != "/api/sessions/sess-1/attach/ws" {
		t.Fatalf("path = %q, want attach path", got)
	}
	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/attach/ws")
	if got := int(logNumber(t, access, "status")); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}

func doBearerGET(t *testing.T, target, accessToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Authorization", bearerAuth(accessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	return resp
}

func doBearerPOST(t *testing.T, target, accessToken, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Authorization", bearerAuth(accessToken))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	return resp
}

func doJSONPOST(t *testing.T, target, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	return resp
}

func readAgentFrame(t *testing.T, conn *websocket.Conn) protocol.AgentFrame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frame protocol.AgentFrame
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	return frame
}

func readAttachControl(t *testing.T, conn *websocket.Conn) protocol.AttachControlMessage {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage returned error: %v", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var msg protocol.AttachControlMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		return msg
	}
}

func readAttachBinary(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage returned error: %v", err)
		}
		if messageType == websocket.BinaryMessage {
			return payload
		}
	}
}

func bearerAuth(token string) string {
	return "Bearer " + token
}

func dialAndRegisterAgent(t *testing.T, serverURL, agentToken, sessionID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(agentToken))

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	if err := conn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID:      sessionID,
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      10,
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	return conn
}

func dialAttachClient(t *testing.T, serverURL, accessToken, sessionID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/sessions/" + sessionID + "/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(accessToken))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial attach returned error: %v", err)
	}
	return conn
}
