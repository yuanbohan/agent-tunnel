package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	relayconfig "yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relay/auth"
	handlerapi "yuanbohan/tunnel/internal/relay/handler/api"
	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
	relayoperator "yuanbohan/tunnel/internal/relay/operator"
	relaysession "yuanbohan/tunnel/internal/relay/session"
)

type SecretDigester = auth.SecretDigester
type PasswordHasher = auth.PasswordHasher
type AppAuthService = auth.AppAuthService
type AgentTokenService = auth.AgentTokenService
type OperatorService = relayoperator.OperatorService
type Registry = relaysession.Registry
type User = auth.User
type IssuedAppSession = auth.IssuedAppSession
type CreatedAgentToken = auth.CreatedAgentToken
type AgentTokenRecord = auth.AgentTokenRecord
type CreateInviteCodeParams = auth.CreateInviteCodeParams
type SessionOwner = relaysession.SessionOwner
type RegisterThrottle = handlerapi.RegisterThrottle
type appSessionResponse = handlertypes.AppSessionResponse
type agentTokenResponse = handlertypes.AgentTokenResponse
type createdAgentTokenResponse = handlertypes.CreatedAgentTokenResponse

const (
	OperatorInviteCodesPath   = handlertypes.OperatorInviteCodesPath
	OperatorInviteDisablePath = handlertypes.OperatorInviteDisablePath
	OperatorDeleteUserPath    = handlertypes.OperatorDeleteUserPath
)

var (
	NewSecretDigester    = auth.NewSecretDigester
	NewAppAuthService    = auth.NewAppAuthService
	NewAgentTokenService = auth.NewAgentTokenService
	NewOperatorService   = relayoperator.NewOperatorService
	NewRegisterThrottle  = handlerapi.NewRegisterThrottle
	NewRegistry          = relaysession.NewRegistry
	ErrAgentTokenRevoked = auth.ErrAgentTokenRevoked
	errAgentPeerInactive = relaysession.ErrAgentPeerInactive
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendJSON(any) error { return nil }
func (fakeAgentPeer) Close() error       { return nil }

type recordingAttachPeer struct {
	mu          sync.Mutex
	controls    []protocol.AttachControlMessage
	binaries    [][]byte
	closeReason []string
}

func (p *recordingAttachPeer) SendControl(msg protocol.AttachControlMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.controls = append(p.controls, msg)
	return nil
}

func (p *recordingAttachPeer) SendBinary(payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binaries = append(p.binaries, append([]byte(nil), payload...))
	return nil
}

func (p *recordingAttachPeer) Close(reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeReason = append(p.closeReason, reason)
	return nil
}

func (p *recordingAttachPeer) CloseReasons() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.closeReason...)
}

type fakeStore struct {
	now                func() time.Time
	nextUserID         int64
	usersByName        map[string]auth.User
	usersByID          map[int64]auth.User
	invites            map[string]auth.InviteCodeRecord
	sessionsByID       map[string]auth.AppSession
	sessionIDByAccess  map[string]string
	sessionIDByRefresh map[string]string
	agentTokensByID    map[string]auth.AgentTokenRecord
	agentTokenByDigest map[string]string
	auditEvents        []auth.AuditEvent
}

func newFakeStore(now func() time.Time) *fakeStore {
	return &fakeStore{
		now:                now,
		nextUserID:         1,
		usersByName:        make(map[string]auth.User),
		usersByID:          make(map[int64]auth.User),
		invites:            make(map[string]auth.InviteCodeRecord),
		sessionsByID:       make(map[string]auth.AppSession),
		sessionIDByAccess:  make(map[string]string),
		sessionIDByRefresh: make(map[string]string),
		agentTokensByID:    make(map[string]auth.AgentTokenRecord),
		agentTokenByDigest: make(map[string]string),
	}
}

func (s *fakeStore) RegisterUser(_ context.Context, params auth.RegisterUserParams) (auth.User, error) {
	invite, ok := s.invites[params.InviteCodeDigest]
	if !ok {
		return auth.User{}, auth.ErrInviteCodeNotFound
	}
	if invite.DisabledAt != nil {
		return auth.User{}, auth.ErrInviteCodeDisabled
	}
	if invite.ConsumedAt != nil {
		return auth.User{}, auth.ErrInviteCodeConsumed
	}
	if !invite.ExpiresAt.After(params.Now) {
		return auth.User{}, auth.ErrInviteCodeExpired
	}
	if _, exists := s.usersByName[params.UsernameNorm]; exists {
		return auth.User{}, auth.ErrUsernameTaken
	}
	user := auth.User{
		ID:           s.nextUserID,
		Username:     params.UsernameNorm,
		UsernameNorm: params.UsernameNorm,
		PasswordHash: params.PasswordHash,
		CreatedAt:    params.Now,
		UpdatedAt:    params.Now,
	}
	s.nextUserID++
	s.usersByName[user.UsernameNorm] = user
	s.usersByID[user.ID] = user
	invite.ConsumedAt = timePtr(params.Now)
	invite.ConsumedByUserID = int64Ptr(user.ID)
	s.invites[params.InviteCodeDigest] = invite
	return user, nil
}

func (s *fakeStore) FindUserByUsername(_ context.Context, usernameNorm string) (auth.User, error) {
	user, ok := s.usersByName[usernameNorm]
	if !ok {
		return auth.User{}, auth.ErrUserNotFound
	}
	return user, nil
}

func (s *fakeStore) FindUserByID(_ context.Context, userID int64) (auth.User, error) {
	user, ok := s.usersByID[userID]
	if !ok {
		return auth.User{}, auth.ErrUserNotFound
	}
	return user, nil
}

func (s *fakeStore) ChangeUserPassword(_ context.Context, userID int64, passwordHash string, now time.Time) error {
	user, ok := s.usersByID[userID]
	if !ok {
		return auth.ErrUserNotFound
	}
	user.PasswordHash = passwordHash
	user.UpdatedAt = now
	s.usersByID[userID] = user
	s.usersByName[user.UsernameNorm] = user
	for id, session := range s.sessionsByID {
		if session.UserID != userID {
			continue
		}
		session.RevokedAt = timePtr(now)
		session.RevokeReason = "password_changed"
		session.UpdatedAt = now
		s.sessionsByID[id] = session
	}
	return nil
}

func (s *fakeStore) CreateAppSession(_ context.Context, params auth.CreateAppSessionParams) (auth.AppSession, error) {
	session := auth.AppSession{
		ID:                 params.ID,
		UserID:             params.UserID,
		AccessTokenDigest:  params.AccessTokenDigest,
		AccessExpiresAt:    params.AccessExpiresAt,
		RefreshTokenDigest: params.RefreshTokenDigest,
		RefreshExpiresAt:   params.RefreshExpiresAt,
		CreatedAt:          params.Now,
		UpdatedAt:          params.Now,
	}
	s.sessionsByID[session.ID] = session
	s.sessionIDByAccess[session.AccessTokenDigest] = session.ID
	s.sessionIDByRefresh[session.RefreshTokenDigest] = session.ID
	return session, nil
}

func (s *fakeStore) FindAppSessionByAccessToken(_ context.Context, accessTokenDigest string, now time.Time) (auth.AppSession, error) {
	id, ok := s.sessionIDByAccess[accessTokenDigest]
	if !ok {
		return auth.AppSession{}, auth.ErrAppSessionNotFound
	}
	session := s.sessionsByID[id]
	if session.RevokedAt != nil {
		return auth.AppSession{}, auth.ErrAppSessionRevoked
	}
	if !session.AccessExpiresAt.After(now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	return session, nil
}

func (s *fakeStore) RotateAppSessionByRefreshToken(_ context.Context, params auth.RotateAppSessionParams) (auth.AppSession, error) {
	id, ok := s.sessionIDByRefresh[params.RefreshTokenDigest]
	if !ok {
		return auth.AppSession{}, auth.ErrAppSessionNotFound
	}
	session := s.sessionsByID[id]
	if session.RevokedAt != nil {
		return auth.AppSession{}, auth.ErrAppSessionRevoked
	}
	if !session.RefreshExpiresAt.After(params.Now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	delete(s.sessionIDByAccess, session.AccessTokenDigest)
	delete(s.sessionIDByRefresh, session.RefreshTokenDigest)
	session.AccessTokenDigest = params.NewAccessTokenDigest
	session.AccessExpiresAt = params.NewAccessExpiresAt
	session.RefreshTokenDigest = params.NewRefreshTokenDigest
	session.RefreshExpiresAt = params.NewRefreshExpiresAt
	session.UpdatedAt = params.Now
	s.sessionsByID[id] = session
	s.sessionIDByAccess[session.AccessTokenDigest] = id
	s.sessionIDByRefresh[session.RefreshTokenDigest] = id
	return session, nil
}

func (s *fakeStore) RevokeAppSession(_ context.Context, sessionID string, now time.Time, reason string) error {
	session, ok := s.sessionsByID[sessionID]
	if !ok {
		return auth.ErrAppSessionNotFound
	}
	session.RevokedAt = timePtr(now)
	session.RevokeReason = reason
	session.UpdatedAt = now
	s.sessionsByID[sessionID] = session
	return nil
}

func (s *fakeStore) CreateInviteCode(_ context.Context, params auth.CreateInviteCodeParams) (auth.InviteCodeRecord, error) {
	record := auth.InviteCodeRecord{
		ID:         int64(len(s.invites) + 1),
		CodeDigest: params.CodeDigest,
		CodeHint:   params.CodeHint,
		CreatedBy:  params.CreatedBy,
		CreatedAt:  params.Now,
		ExpiresAt:  params.ExpiresAt,
	}
	s.invites[params.CodeDigest] = record
	return record, nil
}

func (s *fakeStore) DisableInviteCode(_ context.Context, codeDigest string, actor string, now time.Time) error {
	record, ok := s.invites[codeDigest]
	if !ok {
		return auth.ErrInviteCodeNotFound
	}
	record.DisabledAt = timePtr(now)
	record.DisabledBy = actor
	s.invites[codeDigest] = record
	return nil
}

func (s *fakeStore) CreateAgentToken(_ context.Context, params auth.CreateAgentTokenParams) (auth.AgentTokenRecord, error) {
	record := auth.AgentTokenRecord{
		ID:          params.ID,
		UserID:      params.UserID,
		Name:        params.Name,
		TokenDigest: params.TokenDigest,
		CreatedAt:   params.Now,
		UpdatedAt:   params.Now,
	}
	s.agentTokensByID[record.ID] = record
	s.agentTokenByDigest[record.TokenDigest] = record.ID
	return record, nil
}

func (s *fakeStore) ListAgentTokens(_ context.Context, userID int64) ([]auth.AgentTokenRecord, error) {
	var out []auth.AgentTokenRecord
	for _, token := range s.agentTokensByID {
		if token.UserID == userID {
			out = append(out, token)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *fakeStore) AuthenticateAgentToken(_ context.Context, tokenDigest string, now time.Time) (auth.AgentTokenRecord, error) {
	id, ok := s.agentTokenByDigest[tokenDigest]
	if !ok {
		return auth.AgentTokenRecord{}, auth.ErrAgentTokenNotFound
	}
	record := s.agentTokensByID[id]
	if record.RevokedAt != nil {
		return auth.AgentTokenRecord{}, auth.ErrAgentTokenRevoked
	}
	record.LastUsedAt = timePtr(now)
	record.UpdatedAt = now
	s.agentTokensByID[id] = record
	return record, nil
}

func (s *fakeStore) RevokeAgentToken(_ context.Context, userID int64, tokenID string, actor string, now time.Time) error {
	record, ok := s.agentTokensByID[tokenID]
	if !ok || record.UserID != userID {
		return auth.ErrAgentTokenNotFound
	}
	record.RevokedAt = timePtr(now)
	record.RevokeReason = "revoked_by_user"
	record.UpdatedAt = now
	s.agentTokensByID[tokenID] = record
	s.auditEvents = append(s.auditEvents, auth.AuditEvent{
		ID:                 int64(len(s.auditEvents) + 1),
		EventType:          "agent_token_revoked",
		Actor:              actor,
		TargetUserID:       int64Ptr(userID),
		TargetAgentTokenID: tokenID,
		MetadataJSON:       `{"reason":"revoked_by_user"}`,
		CreatedAt:          now,
	})
	return nil
}

func (s *fakeStore) DeleteUser(_ context.Context, usernameNorm string, actor string, now time.Time) (auth.DeleteUserResult, error) {
	user, ok := s.usersByName[usernameNorm]
	if !ok {
		return auth.DeleteUserResult{}, auth.ErrUserNotFound
	}
	delete(s.usersByName, usernameNorm)
	delete(s.usersByID, user.ID)
	s.auditEvents = append(s.auditEvents, auth.AuditEvent{
		ID:             int64(len(s.auditEvents) + 1),
		EventType:      "user_deleted",
		Actor:          actor,
		TargetUserID:   int64Ptr(user.ID),
		TargetUsername: user.UsernameNorm,
		MetadataJSON:   `{"reason":"operator_delete"}`,
		CreatedAt:      now,
	})
	return auth.DeleteUserResult{UserID: user.ID, UsernameNorm: user.UsernameNorm}, nil
}

type handlerTestEnv struct {
	t           *testing.T
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
	agentTokens := NewAgentTokenService(store, digester)
	operator := NewOperatorService(store, digester)
	throttle := NewRegisterThrottle(5, 10*time.Minute)
	throttle.SetNowFunc(func() time.Time { return now })

	return &handlerTestEnv{
		t:           t,
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

func (e *handlerTestEnv) handler(logWriter io.Writer) http.Handler {
	e.t.Helper()

	if logWriter == nil {
		logWriter = io.Discard
	}
	restoreLogs := logx.UseWriterForTest(logWriter)
	e.t.Cleanup(restoreLogs)

	restoreConfig := relayconfig.UseRelayForTest(relayconfig.Relay{
		OperatorToken: e.operatorTok,
	})
	e.t.Cleanup(restoreConfig)

	return newRouter(e.registry, e.appAuth, e.agentTokens, e.operator, e.throttle)
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

func bearerAuth(token string) string {
	return "Bearer " + token
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

func timePtr(t time.Time) *time.Time { return &t }

func int64Ptr(v int64) *int64 { return &v }
