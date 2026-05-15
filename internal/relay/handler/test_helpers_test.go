package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	relaydevice "yuanbohan/tunnel/internal/relay/device"
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
type DeviceRegistry = relaydevice.Registry
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
	OperatorInviteListPath    = handlertypes.OperatorInviteListPath
	OperatorDeleteUserPath    = handlertypes.OperatorDeleteUserPath
	OperatorUserTierPath      = handlertypes.OperatorUserTierPath
)

var (
	NewSecretDigester    = auth.NewSecretDigester
	NewAppAuthService    = auth.NewAppAuthService
	NewAgentTokenService = auth.NewAgentTokenService
	NewOperatorService   = relayoperator.NewOperatorService
	NewRegisterThrottle  = handlerapi.NewRegisterThrottle
	NewRegistry          = relaysession.NewRegistry
	NewDeviceRegistry    = relaydevice.NewRegistry
	ErrAgentTokenRevoked = auth.ErrAgentTokenRevoked
	errAgentPeerInactive = relaysession.ErrAgentPeerInactive
)

type fakeAgentPeer struct{}

func (fakeAgentPeer) SendJSON(any) error { return nil }
func (fakeAgentPeer) Close() error       { return nil }

type recordingAgentPeer struct {
	mu     sync.Mutex
	frames []protocol.AgentFrame
}

func (p *recordingAgentPeer) SendJSON(msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if frame, ok := msg.(protocol.AgentFrame); ok {
		p.frames = append(p.frames, frame)
	}
	return nil
}

func (p *recordingAgentPeer) Close() error { return nil }

func (p *recordingAgentPeer) Frames() []protocol.AgentFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.AgentFrame(nil), p.frames...)
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
	invite, ok := s.invites[params.InviteCode]
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
		ID:               s.nextUserID,
		Username:         params.UsernameNorm,
		UsernameNorm:     params.UsernameNorm,
		PasswordHash:     params.PasswordHash,
		SubscriptionTier: auth.SubscriptionTierFree,
		CreatedAt:        params.Now,
		UpdatedAt:        params.Now,
	}
	s.nextUserID++
	s.usersByName[user.UsernameNorm] = user
	s.usersByID[user.ID] = user
	invite.ConsumedAt = timePtr(params.Now)
	invite.ConsumedByUserID = int64Ptr(user.ID)
	invite.ConsumedByUsername = user.UsernameNorm
	s.invites[params.InviteCode] = invite
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
		DeviceFingerprint:  params.DeviceFingerprint,
		CreatedAt:          params.Now,
		UpdatedAt:          params.Now,
	}
	s.sessionsByID[session.ID] = session
	s.sessionIDByAccess[session.AccessTokenDigest] = session.ID
	s.sessionIDByRefresh[session.RefreshTokenDigest] = session.ID
	return session, nil
}

func (s *fakeStore) FindAppSessionByAccessToken(_ context.Context, accessTokenDigest string, now time.Time, absoluteTTL time.Duration) (auth.AppSession, error) {
	id, ok := s.sessionIDByAccess[accessTokenDigest]
	if !ok {
		return auth.AppSession{}, auth.ErrAppSessionNotFound
	}
	session := s.sessionsByID[id]
	if session.RevokedAt != nil {
		return auth.AppSession{}, auth.ErrAppSessionRevoked
	}
	if absoluteTTL > 0 && !session.CreatedAt.Add(absoluteTTL).After(now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
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
	if params.AbsoluteTTL > 0 && !session.CreatedAt.Add(params.AbsoluteTTL).After(params.Now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	if !session.RefreshExpiresAt.After(params.Now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	if session.DeviceFingerprint != "" && session.DeviceFingerprint != params.DeviceFingerprint {
		return auth.AppSession{}, auth.ErrAppSessionDeviceMismatch
	}
	if params.AbsoluteTTL > 0 {
		absoluteExpiresAt := session.CreatedAt.Add(params.AbsoluteTTL)
		if params.NewAccessExpiresAt.After(absoluteExpiresAt) {
			params.NewAccessExpiresAt = absoluteExpiresAt
		}
		if params.NewRefreshExpiresAt.After(absoluteExpiresAt) {
			params.NewRefreshExpiresAt = absoluteExpiresAt
		}
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
		ID:        int64(len(s.invites) + 1),
		Code:      params.Code,
		CreatedBy: params.CreatedBy,
		CreatedAt: params.Now,
		ExpiresAt: params.ExpiresAt,
	}
	s.invites[params.Code] = record
	return record, nil
}

func (s *fakeStore) ListInviteCodes(_ context.Context) ([]auth.InviteCodeRecord, error) {
	out := make([]auth.InviteCodeRecord, 0, len(s.invites))
	for _, invite := range s.invites {
		out = append(out, invite)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *fakeStore) CreateInviteCodes(ctx context.Context, params []auth.CreateInviteCodeParams) error {
	for _, param := range params {
		if _, err := s.CreateInviteCode(ctx, param); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeStore) DisableInviteCode(_ context.Context, code string, actor string, now time.Time) error {
	record, ok := s.invites[code]
	if !ok {
		return auth.ErrInviteCodeNotFound
	}
	record.DisabledAt = timePtr(now)
	record.DisabledBy = actor
	s.invites[code] = record
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

func (s *fakeStore) SetUserSubscriptionTier(_ context.Context, usernameNorm string, tier string, actor string, now time.Time) (auth.User, string, error) {
	user, ok := s.usersByName[usernameNorm]
	if !ok {
		return auth.User{}, "", auth.ErrUserNotFound
	}
	previous := user.SubscriptionTier
	if previous == "" {
		previous = auth.SubscriptionTierFree
	}
	user.SubscriptionTier = tier
	user.UpdatedAt = now
	s.usersByName[usernameNorm] = user
	s.usersByID[user.ID] = user
	s.auditEvents = append(s.auditEvents, auth.AuditEvent{
		ID:             int64(len(s.auditEvents) + 1),
		EventType:      "user_subscription_tier_changed",
		Actor:          actor,
		TargetUserID:   int64Ptr(user.ID),
		TargetUsername: user.UsernameNorm,
		MetadataJSON:   `{"new_tier":"` + tier + `","previous_tier":"` + previous + `"}`,
		CreatedAt:      now,
	})
	return user, previous, nil
}

type handlerTestEnv struct {
	t              *testing.T
	now            time.Time
	store          *fakeStore
	digester       *SecretDigester
	appAuth        *AppAuthService
	agentTokens    *AgentTokenService
	operator       *OperatorService
	operatorTok    string
	throttle       *RegisterThrottle
	registry       *Registry
	deviceRegistry *DeviceRegistry
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
	operator := NewOperatorService(store)
	throttle := NewRegisterThrottle(5, 10*time.Minute)
	throttle.SetNowFunc(func() time.Time { return now })

	env := &handlerTestEnv{
		t:              t,
		now:            now,
		store:          store,
		digester:       digester,
		appAuth:        appAuth,
		agentTokens:    agentTokens,
		operator:       operator,
		operatorTok:    "operator-secret",
		throttle:       throttle,
		registry:       NewRegistry(),
		deviceRegistry: NewDeviceRegistry(),
	}
	env.appAuth.SetNowFunc(func() time.Time { return env.now })
	env.throttle.SetNowFunc(func() time.Time { return env.now })
	return env
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

	return newRouter(e.registry, e.deviceRegistry, e.appAuth, e.agentTokens, e.operator, e.throttle)
}

func (e *handlerTestEnv) addInvite(t *testing.T, code string) {
	t.Helper()
	if _, err := e.store.CreateInviteCode(context.Background(), CreateInviteCodeParams{
		Code:      code,
		CreatedBy: "tester",
		ExpiresAt: e.now.Add(24 * time.Hour),
		Now:       e.now,
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

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Body    json.RawMessage `json:"body"`
}

func decodeAPIEnvelope(t *testing.T, statusCode int, payload []byte, wantStatus int, out any) {
	t.Helper()

	if statusCode != wantStatus {
		t.Fatalf("status = %d, want %d", statusCode, wantStatus)
	}

	var env apiEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode response envelope: %v, body=%q", err, strings.TrimSpace(string(payload)))
	}
	if env.Code != 0 {
		t.Fatalf("api code = %d, message = %q", env.Code, env.Message)
	}
	if env.Message != "success" {
		t.Fatalf("api success message = %q, want success", env.Message)
	}
	if out == nil {
		if strings.TrimSpace(string(env.Body)) != "null" {
			t.Fatalf("response body = %q, want null", strings.TrimSpace(string(env.Body)))
		}
		return
	}
	if len(env.Body) == 0 || strings.TrimSpace(string(env.Body)) == "null" {
		t.Fatalf("response body is empty or null, want business payload")
	}
	if err := json.Unmarshal(env.Body, out); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func decodeAPIEnvelopeFromResponse(t *testing.T, response *http.Response, wantStatus int, out any) {
	t.Helper()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	decodeAPIEnvelope(t, response.StatusCode, payload, wantStatus, out)
}

func decodeAPIEnvelopeFromRecorder(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, out any) {
	t.Helper()
	decodeAPIEnvelope(t, recorder.Code, recorder.Body.Bytes(), wantStatus, out)
}

func decodeAPIErrorEnvelopeFromResponse(t *testing.T, response *http.Response, wantStatus, wantCode int, wantMessage string) {
	t.Helper()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	decodeAPIErrorEnvelope(t, response.StatusCode, payload, wantStatus, wantCode, wantMessage)
}

func decodeAPIErrorEnvelopeFromRecorder(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus, wantCode int, wantMessage string) {
	t.Helper()
	decodeAPIErrorEnvelope(t, recorder.Code, recorder.Body.Bytes(), wantStatus, wantCode, wantMessage)
}

func decodeAPIErrorEnvelope(t *testing.T, statusCode int, payload []byte, wantStatus, wantCode int, wantMessage string) {
	t.Helper()

	if statusCode != wantStatus {
		t.Fatalf("status = %d, want %d", statusCode, wantStatus)
	}

	var env apiEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode error envelope: %v, body=%q", err, strings.TrimSpace(string(payload)))
	}
	if env.Code != wantCode {
		t.Fatalf("api error code = %d, want %d", env.Code, wantCode)
	}
	if env.Message != wantMessage {
		t.Fatalf("api error message = %q, want %q", env.Message, wantMessage)
	}
	if strings.TrimSpace(string(env.Body)) != "null" {
		t.Fatalf("error response body = %q, want null", strings.TrimSpace(string(env.Body)))
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

func doBearerDELETE(t *testing.T, target, accessToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, target, nil)
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

func dialAndRegisterAgent(t *testing.T, serverURL, agentToken, sessionID string) *websocket.Conn {
	t.Helper()
	return dialAndRegisterAgentWithLaunchRequest(t, serverURL, agentToken, sessionID, "")
}

func dialAndRegisterAgentWithLaunchRequest(t *testing.T, serverURL, agentToken, sessionID, launchRequestID string) *websocket.Conn {
	return dialAndRegisterAgentWithLaunchRequestAndDeviceID(t, serverURL, agentToken, sessionID, launchRequestID, "")
}

func dialAndRegisterAgentWithLaunchRequestAndDeviceID(t *testing.T, serverURL, agentToken, sessionID, launchRequestID, deviceID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(agentToken))

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	launchContext := protocol.LaunchContext{}
	if strings.TrimSpace(launchRequestID) != "" {
		launchContext = protocol.LaunchContext{
			Source:    protocol.SessionLaunchSourceMobile,
			RequestID: strings.TrimSpace(launchRequestID),
		}
	}

	if err := conn.WriteJSON(protocol.RegisterFrameWithLaunchContext(protocol.SessionInfo{
		SessionID:      sessionID,
		DeviceID:       deviceID,
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		GitBranch:      "main",
		StartedAt:      10,
		PlatformFamily: "linux",
		PlatformID:     "ubuntu",
		ComputerName:   "Office Linux",
	}, launchContext)); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	return conn
}

func dialAndRegisterDevice(t *testing.T, serverURL, agentToken string, info protocol.DeviceInfo) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/device/ws"
	headers := http.Header{}
	headers.Set("Authorization", bearerAuth(agentToken))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial device websocket returned error: %v", err)
	}
	if err := conn.WriteJSON(protocol.DeviceRegisterFrame(info)); err != nil {
		t.Fatalf("WriteJSON device register returned error: %v", err)
	}
	return conn
}

type fakeDevicePeerForHandler struct{}

func (fakeDevicePeerForHandler) SendJSON(any) error { return nil }
func (fakeDevicePeerForHandler) Close() error       { return nil }

type blockingDevicePeer struct {
	sent chan protocol.DeviceFrame
}

func (p *blockingDevicePeer) SendJSON(v any) error {
	frame, _ := v.(protocol.DeviceFrame)
	p.sent <- frame
	return nil
}

func (p *blockingDevicePeer) Close() error { return nil }

func waitForOwnedSession(t *testing.T, registry *Registry, sessionID string, userID int64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if registry.HasSession(sessionID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("session %q for user %d was not registered before timeout", sessionID, userID)
}

func timePtr(t time.Time) *time.Time { return &t }

func int64Ptr(v int64) *int64 { return &v }
