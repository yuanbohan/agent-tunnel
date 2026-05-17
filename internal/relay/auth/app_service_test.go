package auth

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	now                func() time.Time
	nextUserID         int64
	usersByName        map[string]User
	usersByID          map[int64]User
	invites            map[string]InviteCodeRecord
	sessionsByID       map[string]AppSession
	sessionIDByAccess  map[string]string
	sessionIDByRefresh map[string]string
	agentTokensByID    map[string]AgentTokenRecord
	agentTokenByDigest map[string]string
	auditEvents        []AuditEvent
}

func newFakeStore(now func() time.Time) *fakeStore {
	return &fakeStore{
		now:                now,
		nextUserID:         1,
		usersByName:        make(map[string]User),
		usersByID:          make(map[int64]User),
		invites:            make(map[string]InviteCodeRecord),
		sessionsByID:       make(map[string]AppSession),
		sessionIDByAccess:  make(map[string]string),
		sessionIDByRefresh: make(map[string]string),
		agentTokensByID:    make(map[string]AgentTokenRecord),
		agentTokenByDigest: make(map[string]string),
	}
}

func (s *fakeStore) RegisterUser(_ context.Context, params RegisterUserParams) (User, error) {
	invite, ok := s.invites[params.InviteCode]
	if !ok {
		return User{}, ErrInviteCodeNotFound
	}
	if invite.DisabledAt != nil {
		return User{}, ErrInviteCodeDisabled
	}
	if invite.ConsumedAt != nil {
		return User{}, ErrInviteCodeConsumed
	}
	if !invite.ExpiresAt.After(params.Now) {
		return User{}, ErrInviteCodeExpired
	}
	if _, exists := s.usersByName[params.UsernameNorm]; exists {
		return User{}, ErrUsernameTaken
	}
	user := User{
		ID:               s.nextUserID,
		Username:         params.UsernameNorm,
		UsernameNorm:     params.UsernameNorm,
		PasswordHash:     params.PasswordHash,
		SubscriptionTier: SubscriptionTierFree,
		CreatedAt:        params.Now,
		UpdatedAt:        params.Now,
	}
	s.nextUserID++
	s.usersByName[user.UsernameNorm] = user
	s.usersByID[user.ID] = user
	invite.ConsumedAt = timePtr(params.Now)
	invite.ConsumedByUserID = int64Ptr(user.ID)
	s.invites[params.InviteCode] = invite
	return user, nil
}

func (s *fakeStore) FindUserByUsername(_ context.Context, usernameNorm string) (User, error) {
	user, ok := s.usersByName[usernameNorm]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *fakeStore) FindUserByID(_ context.Context, userID int64) (User, error) {
	user, ok := s.usersByID[userID]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *fakeStore) ChangeUserPassword(_ context.Context, userID int64, passwordHash string, now time.Time) error {
	user, ok := s.usersByID[userID]
	if !ok {
		return ErrUserNotFound
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

func (s *fakeStore) CreateAppSession(_ context.Context, params CreateAppSessionParams) (AppSession, error) {
	session := AppSession{
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

func (s *fakeStore) FindAppSessionByAccessToken(_ context.Context, accessTokenDigest string, now time.Time, absoluteTTL time.Duration) (AppSession, error) {
	id, ok := s.sessionIDByAccess[accessTokenDigest]
	if !ok {
		return AppSession{}, ErrAppSessionNotFound
	}
	session := s.sessionsByID[id]
	if session.RevokedAt != nil {
		return AppSession{}, ErrAppSessionRevoked
	}
	if absoluteTTL > 0 && !session.CreatedAt.Add(absoluteTTL).After(now) {
		return AppSession{}, ErrAppSessionExpired
	}
	if !session.AccessExpiresAt.After(now) {
		return AppSession{}, ErrAppSessionExpired
	}
	return session, nil
}

func (s *fakeStore) RotateAppSessionByRefreshToken(_ context.Context, params RotateAppSessionParams) (AppSession, error) {
	id, ok := s.sessionIDByRefresh[params.RefreshTokenDigest]
	if !ok {
		return AppSession{}, ErrAppSessionNotFound
	}
	session := s.sessionsByID[id]
	if session.RevokedAt != nil {
		return AppSession{}, ErrAppSessionRevoked
	}
	if params.AbsoluteTTL > 0 && !session.CreatedAt.Add(params.AbsoluteTTL).After(params.Now) {
		return AppSession{}, ErrAppSessionExpired
	}
	if !session.RefreshExpiresAt.After(params.Now) {
		return AppSession{}, ErrAppSessionExpired
	}
	if session.DeviceFingerprint != "" && session.DeviceFingerprint != params.DeviceFingerprint {
		return AppSession{}, ErrAppSessionDeviceMismatch
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
		return ErrAppSessionNotFound
	}
	session.RevokedAt = timePtr(now)
	session.RevokeReason = reason
	session.UpdatedAt = now
	s.sessionsByID[sessionID] = session
	return nil
}

func (s *fakeStore) CreateInviteCode(_ context.Context, params CreateInviteCodeParams) (InviteCodeRecord, error) {
	record := InviteCodeRecord{
		ID:        int64(len(s.invites) + 1),
		Code:      params.Code,
		CreatedBy: params.CreatedBy,
		CreatedAt: params.Now,
		ExpiresAt: params.ExpiresAt,
	}
	s.invites[params.Code] = record
	return record, nil
}

func (s *fakeStore) DisableInviteCode(_ context.Context, code string, actor string, now time.Time) error {
	record, ok := s.invites[code]
	if !ok {
		return ErrInviteCodeNotFound
	}
	record.DisabledAt = timePtr(now)
	record.DisabledBy = actor
	s.invites[code] = record
	return nil
}

func (s *fakeStore) CreateAgentToken(_ context.Context, params CreateAgentTokenParams) (AgentTokenRecord, error) {
	record := AgentTokenRecord{
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

func (s *fakeStore) ListAgentTokens(_ context.Context, userID int64) ([]AgentTokenRecord, error) {
	var out []AgentTokenRecord
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

func (s *fakeStore) AuthenticateAgentToken(_ context.Context, tokenDigest string, now time.Time) (AgentTokenRecord, error) {
	id, ok := s.agentTokenByDigest[tokenDigest]
	if !ok {
		return AgentTokenRecord{}, ErrAgentTokenNotFound
	}
	record := s.agentTokensByID[id]
	if record.RevokedAt != nil {
		return AgentTokenRecord{}, ErrAgentTokenRevoked
	}
	record.LastUsedAt = timePtr(now)
	record.UpdatedAt = now
	s.agentTokensByID[id] = record
	return record, nil
}

func (s *fakeStore) RevokeAgentToken(_ context.Context, userID int64, tokenID string, actor string, now time.Time) error {
	record, ok := s.agentTokensByID[tokenID]
	if !ok || record.UserID != userID {
		return ErrAgentTokenNotFound
	}
	record.RevokedAt = timePtr(now)
	record.RevokeReason = "revoked_by_user"
	record.UpdatedAt = now
	s.agentTokensByID[tokenID] = record
	s.auditEvents = append(s.auditEvents, AuditEvent{
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

func (s *fakeStore) DeleteUser(_ context.Context, usernameNorm string, actor string, now time.Time) (DeleteUserResult, error) {
	user, ok := s.usersByName[usernameNorm]
	if !ok {
		return DeleteUserResult{}, ErrUserNotFound
	}
	delete(s.usersByName, usernameNorm)
	delete(s.usersByID, user.ID)
	s.auditEvents = append(s.auditEvents, AuditEvent{
		ID:             int64(len(s.auditEvents) + 1),
		EventType:      "user_deleted",
		Actor:          actor,
		TargetUserID:   int64Ptr(user.ID),
		TargetUsername: user.UsernameNorm,
		MetadataJSON:   `{"reason":"operator_delete"}`,
		CreatedAt:      now,
	})
	return DeleteUserResult{UserID: user.ID, UsernameNorm: user.UsernameNorm}, nil
}

func TestAppAuthServiceRegisterAndLogin(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	store := newFakeStore(func() time.Time { return now })
	digester, err := NewSecretDigester("test-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	code := "AB2C3D"
	if _, err := store.CreateInviteCode(context.Background(), CreateInviteCodeParams{
		Code:      code,
		CreatedBy: "tester",
		ExpiresAt: now.Add(time.Hour),
		Now:       now,
	}); err != nil {
		t.Fatalf("CreateInviteCode returned error: %v", err)
	}

	service := NewAppAuthService(store, digester, PasswordHasher{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	})
	service.now = func() time.Time { return now }

	user, err := service.Register(context.Background(), "Alice", "password123", code)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if user.UsernameNorm != "alice" {
		t.Fatalf("UsernameNorm = %q, want alice", user.UsernameNorm)
	}

	issued, err := service.Login(context.Background(), "ALICE", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if issued.AccessToken == "" || issued.RefreshToken == "" {
		t.Fatal("expected access and refresh token")
	}
	if issued.ExpiresIn != DefaultAccessTokenTTL {
		t.Fatalf("ExpiresIn = %s, want %s", issued.ExpiresIn, DefaultAccessTokenTTL)
	}
}

func TestAppAuthServiceBindsRefreshToDeviceFingerprint(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	store := newFakeStore(func() time.Time { return now })
	digester, err := NewSecretDigester("test-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	service := NewAppAuthService(store, digester, PasswordHasher{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	})
	service.now = func() time.Time { return now }

	const code = "AB2C3D"
	if _, err := store.CreateInviteCode(context.Background(), CreateInviteCodeParams{
		Code:      code,
		CreatedBy: "operator",
		ExpiresAt: now.Add(time.Hour),
		Now:       now,
	}); err != nil {
		t.Fatalf("CreateInviteCode returned error: %v", err)
	}
	if _, err := service.Register(context.Background(), "alice", "password123", code); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	fingerprint := strings.Repeat("a", DeviceFingerprintHexLength)
	issued, err := service.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", strings.ToUpper(fingerprint))
	if err != nil {
		t.Fatalf("LoginWithDeviceFingerprint returned error: %v", err)
	}
	if issued.Session.DeviceFingerprint != fingerprint {
		t.Fatalf("DeviceFingerprint = %q, want %q", issued.Session.DeviceFingerprint, fingerprint)
	}

	refreshed, err := service.RefreshWithDeviceFingerprint(context.Background(), issued.RefreshToken, fingerprint)
	if err != nil {
		t.Fatalf("RefreshWithDeviceFingerprint returned error: %v", err)
	}
	if refreshed.Session.DeviceFingerprint != fingerprint {
		t.Fatalf("refreshed DeviceFingerprint = %q, want %q", refreshed.Session.DeviceFingerprint, fingerprint)
	}

	mismatchedFingerprint := strings.Repeat("b", DeviceFingerprintHexLength)
	if _, err := service.RefreshWithDeviceFingerprint(context.Background(), refreshed.RefreshToken, mismatchedFingerprint); !errors.Is(err, ErrAppSessionDeviceMismatch) {
		t.Fatalf("mismatched refresh error = %v, want ErrAppSessionDeviceMismatch", err)
	}
	if _, err := service.LoginWithDeviceFingerprint(context.Background(), "alice", "password123", "not-hex"); !errors.Is(err, ErrInvalidDeviceFingerprint) {
		t.Fatalf("invalid fingerprint login error = %v, want ErrInvalidDeviceFingerprint", err)
	}
}

func TestAppAuthServiceRefreshLogoutAndPasswordChange(t *testing.T) {
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
	passwordHash, err := hasher.HashPassword(context.Background(), "password123")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	user := User{
		ID:           1,
		Username:     "alice",
		UsernameNorm: "alice",
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.usersByName[user.UsernameNorm] = user
	store.usersByID[user.ID] = user

	service := NewAppAuthService(store, digester, hasher)
	service.now = func() time.Time { return now }

	issued, err := service.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	auth, err := service.AuthenticateAccessToken(context.Background(), issued.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateAccessToken returned error: %v", err)
	}

	refreshed, err := service.Refresh(context.Background(), issued.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if refreshed.RefreshToken == issued.RefreshToken {
		t.Fatal("expected refresh token rotation")
	}
	if refreshed.ExpiresIn != DefaultAccessTokenTTL {
		t.Fatalf("Refresh ExpiresIn = %s, want %s", refreshed.ExpiresIn, DefaultAccessTokenTTL)
	}

	if err := service.ChangePassword(context.Background(), auth, "password123", "newpassword123"); err != nil {
		t.Fatalf("ChangePassword returned error: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(context.Background(), refreshed.AccessToken); err == nil {
		t.Fatal("expected access token to fail after password change")
	}

	issuedAgain, err := service.Login(context.Background(), "alice", "newpassword123")
	if err != nil {
		t.Fatalf("Login with new password returned error: %v", err)
	}
	authAgain, err := service.AuthenticateAccessToken(context.Background(), issuedAgain.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateAccessToken returned error: %v", err)
	}
	if err := service.Logout(context.Background(), authAgain); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(context.Background(), issuedAgain.AccessToken); err == nil {
		t.Fatal("expected access token to fail after logout")
	}
}

func TestAppAuthServiceEnforcesAbsoluteSessionLifetime(t *testing.T) {
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
	passwordHash, err := hasher.HashPassword(context.Background(), "password123")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	user := User{
		ID:           1,
		Username:     "alice",
		UsernameNorm: "alice",
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.usersByName[user.UsernameNorm] = user
	store.usersByID[user.ID] = user

	service := NewAppAuthService(store, digester, hasher)
	service.now = func() time.Time { return now }

	issued, err := service.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	now = now.Add(29 * 24 * time.Hour)
	refreshed, err := service.Refresh(context.Background(), issued.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh at 29 days returned error: %v", err)
	}

	now = now.Add(29 * 24 * time.Hour)
	refreshed, err = service.Refresh(context.Background(), refreshed.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh at 58 days returned error: %v", err)
	}

	now = now.Add(29 * 24 * time.Hour)
	refreshed, err = service.Refresh(context.Background(), refreshed.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh at 87 days returned error: %v", err)
	}

	now = now.Add(3*24*time.Hour - time.Hour)
	refreshed, err = service.Refresh(context.Background(), refreshed.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh near absolute limit returned error: %v", err)
	}
	if refreshed.ExpiresIn != time.Hour {
		t.Fatalf("ExpiresIn near absolute limit = %s, want %s", refreshed.ExpiresIn, time.Hour)
	}

	now = now.Add(time.Hour)
	if _, err := service.AuthenticateAccessToken(context.Background(), refreshed.AccessToken); err != ErrAppSessionExpired {
		t.Fatalf("AuthenticateAccessToken at absolute expiry = %v, want ErrAppSessionExpired", err)
	}
	if _, err := service.Refresh(context.Background(), refreshed.RefreshToken); err != ErrAppSessionExpired {
		t.Fatalf("Refresh at absolute expiry = %v, want ErrAppSessionExpired", err)
	}

	now = now.Add(time.Minute)
	if _, err := service.AuthenticateAccessToken(context.Background(), refreshed.AccessToken); err != ErrAppSessionExpired {
		t.Fatalf("AuthenticateAccessToken after absolute expiry = %v, want ErrAppSessionExpired", err)
	}
	if _, err := service.Refresh(context.Background(), refreshed.RefreshToken); err != ErrAppSessionExpired {
		t.Fatalf("Refresh after absolute expiry = %v, want ErrAppSessionExpired", err)
	}
}

func TestAppAuthServicePropagatesInvalidPasswordHashErrors(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	store := newFakeStore(func() time.Time { return now })
	user := User{
		ID:           1,
		Username:     "alice",
		UsernameNorm: "alice",
		PasswordHash: "not-a-valid-password-hash",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.usersByName[user.UsernameNorm] = user
	store.usersByID[user.ID] = user

	digester, err := NewSecretDigester("test-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	service := NewAppAuthService(store, digester, DefaultPasswordHasher())
	service.now = func() time.Time { return now }

	if _, err := service.Login(context.Background(), "alice", "password123"); err != ErrInvalidPasswordHash {
		t.Fatalf("Login error = %v, want ErrInvalidPasswordHash", err)
	}
	if err := service.ChangePassword(context.Background(), AuthenticatedApp{User: user}, "password123", "newpassword123"); err != ErrInvalidPasswordHash {
		t.Fatalf("ChangePassword error = %v, want ErrInvalidPasswordHash", err)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func int64Ptr(v int64) *int64 { return &v }
