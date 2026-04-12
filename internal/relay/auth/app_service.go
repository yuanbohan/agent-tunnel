package auth

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultAccessTokenTTL        = 24 * time.Hour
	DefaultRefreshTokenTTL       = 30 * 24 * time.Hour
	DefaultAppSessionAbsoluteTTL = 90 * 24 * time.Hour
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type IssuedAppSession struct {
	User         User
	Session      AppSession
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

type AuthenticatedApp struct {
	User    User
	Session AppSession
}

type AppAuthService struct {
	store       Repository
	digester    *SecretDigester
	hasher      PasswordHasher
	now         func() time.Time
	accessTTL   time.Duration
	refreshTTL  time.Duration
	absoluteTTL time.Duration
}

func NewAppAuthService(store Repository, digester *SecretDigester, hasher PasswordHasher) *AppAuthService {
	return &AppAuthService{
		store:       store,
		digester:    digester,
		hasher:      hasher,
		now:         func() time.Time { return time.Now().UTC() },
		accessTTL:   DefaultAccessTokenTTL,
		refreshTTL:  DefaultRefreshTokenTTL,
		absoluteTTL: DefaultAppSessionAbsoluteTTL,
	}
}

func (s *AppAuthService) SetNowFunc(now func() time.Time) {
	if now == nil {
		return
	}
	s.now = now
}

func (s *AppAuthService) Register(ctx context.Context, username, password, inviteCode string) (User, error) {
	usernameNorm, err := NormalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	inviteCodeNorm, err := NormalizeInviteCode(inviteCode)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := s.hasher.HashPassword(ctx, password)
	if err != nil {
		return User{}, err
	}
	now := s.now()
	return s.store.RegisterUser(ctx, RegisterUserParams{
		UsernameNorm:     usernameNorm,
		InviteCodeDigest: s.digester.Digest(inviteCodeNorm),
		PasswordHash:     passwordHash,
		Now:              now,
	})
}

func (s *AppAuthService) Login(ctx context.Context, username, password string) (IssuedAppSession, error) {
	usernameNorm, err := NormalizeUsername(username)
	if err != nil {
		return IssuedAppSession{}, ErrInvalidCredentials
	}

	user, err := s.store.FindUserByUsername(ctx, usernameNorm)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return IssuedAppSession{}, ErrInvalidCredentials
		}
		return IssuedAppSession{}, err
	}
	if err := s.hasher.VerifyPassword(password, user.PasswordHash); err != nil {
		if errors.Is(err, ErrInvalidPassword) {
			return IssuedAppSession{}, ErrInvalidCredentials
		}
		return IssuedAppSession{}, err
	}

	return s.issueSession(ctx, user)
}

func (s *AppAuthService) AuthenticateAccessToken(ctx context.Context, accessToken string) (AuthenticatedApp, error) {
	session, err := s.store.FindAppSessionByAccessToken(ctx, s.digester.Digest(accessToken), s.now(), s.absoluteTTL)
	if err != nil {
		return AuthenticatedApp{}, err
	}
	user, err := s.store.FindUserByID(ctx, session.UserID)
	if err != nil {
		return AuthenticatedApp{}, err
	}
	return AuthenticatedApp{User: user, Session: session}, nil
}

func (s *AppAuthService) Refresh(ctx context.Context, refreshToken string) (IssuedAppSession, error) {
	accessToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}
	newRefreshToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}
	now := s.now()

	session, err := s.store.RotateAppSessionByRefreshToken(ctx, RotateAppSessionParams{
		RefreshTokenDigest:    s.digester.Digest(refreshToken),
		NewAccessTokenDigest:  s.digester.Digest(accessToken),
		NewAccessExpiresAt:    now.Add(s.accessTTL),
		NewRefreshTokenDigest: s.digester.Digest(newRefreshToken),
		NewRefreshExpiresAt:   now.Add(s.refreshTTL),
		AbsoluteTTL:           s.absoluteTTL,
		Now:                   now,
	})
	if err != nil {
		return IssuedAppSession{}, err
	}
	user, err := s.store.FindUserByID(ctx, session.UserID)
	if err != nil {
		return IssuedAppSession{}, err
	}
	expiresAtNow := s.now()
	return IssuedAppSession{
		User:         user,
		Session:      session,
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresIn:    session.AccessExpiresAt.Sub(expiresAtNow),
	}, nil
}

func (s *AppAuthService) Logout(ctx context.Context, auth AuthenticatedApp) error {
	return s.store.RevokeAppSession(ctx, auth.Session.ID, s.now(), "logout")
}

func (s *AppAuthService) ChangePassword(ctx context.Context, auth AuthenticatedApp, currentPassword, newPassword string) error {
	if err := s.hasher.VerifyPassword(currentPassword, auth.User.PasswordHash); err != nil {
		if errors.Is(err, ErrInvalidPassword) {
			return ErrInvalidCredentials
		}
		return err
	}
	passwordHash, err := s.hasher.HashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.store.ChangeUserPassword(ctx, auth.User.ID, passwordHash, s.now())
}

func (s *AppAuthService) issueSession(ctx context.Context, user User) (IssuedAppSession, error) {
	sessionID, err := GenerateOpaqueID("appsess", 16)
	if err != nil {
		return IssuedAppSession{}, err
	}
	accessToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}
	refreshToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}
	now := s.now()

	session, err := s.store.CreateAppSession(ctx, CreateAppSessionParams{
		ID:                 sessionID,
		UserID:             user.ID,
		AccessTokenDigest:  s.digester.Digest(accessToken),
		AccessExpiresAt:    now.Add(s.accessTTL),
		RefreshTokenDigest: s.digester.Digest(refreshToken),
		RefreshExpiresAt:   now.Add(s.refreshTTL),
		Now:                now,
	})
	if err != nil {
		return IssuedAppSession{}, err
	}
	expiresAtNow := s.now()

	return IssuedAppSession{
		User:         user,
		Session:      session,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    session.AccessExpiresAt.Sub(expiresAtNow),
	}, nil
}
