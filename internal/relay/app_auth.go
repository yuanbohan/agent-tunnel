package relay

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultAccessTokenTTL  = 15 * time.Minute
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type IssuedAppSession struct {
	User         User
	Session      AppSession
	AccessToken  string
	RefreshToken string
}

type AuthenticatedApp struct {
	User    User
	Session AppSession
}

type AppAuthService struct {
	store      Store
	digester   *SecretDigester
	hasher     PasswordHasher
	now        func() time.Time
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewAppAuthService(store Store, digester *SecretDigester, hasher PasswordHasher) *AppAuthService {
	return &AppAuthService{
		store:      store,
		digester:   digester,
		hasher:     hasher,
		now:        func() time.Time { return time.Now().UTC() },
		accessTTL:  DefaultAccessTokenTTL,
		refreshTTL: DefaultRefreshTokenTTL,
	}
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
		return IssuedAppSession{}, ErrInvalidCredentials
	}

	return s.issueSession(ctx, user)
}

func (s *AppAuthService) AuthenticateAccessToken(ctx context.Context, accessToken string) (AuthenticatedApp, error) {
	session, err := s.store.FindAppSessionByAccessToken(ctx, s.digester.Digest(accessToken), s.now())
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
	now := s.now()
	accessToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}
	newRefreshToken, err := GenerateOpaqueToken(32)
	if err != nil {
		return IssuedAppSession{}, err
	}

	session, err := s.store.RotateAppSessionByRefreshToken(ctx, RotateAppSessionParams{
		RefreshTokenDigest:    s.digester.Digest(refreshToken),
		NewAccessTokenDigest:  s.digester.Digest(accessToken),
		NewAccessExpiresAt:    now.Add(s.accessTTL),
		NewRefreshTokenDigest: s.digester.Digest(newRefreshToken),
		NewRefreshExpiresAt:   now.Add(s.refreshTTL),
		Now:                   now,
	})
	if err != nil {
		return IssuedAppSession{}, err
	}
	user, err := s.store.FindUserByID(ctx, session.UserID)
	if err != nil {
		return IssuedAppSession{}, err
	}
	return IssuedAppSession{
		User:         user,
		Session:      session,
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

func (s *AppAuthService) Logout(ctx context.Context, auth AuthenticatedApp) error {
	return s.store.RevokeAppSession(ctx, auth.Session.ID, s.now(), "logout")
}

func (s *AppAuthService) ChangePassword(ctx context.Context, auth AuthenticatedApp, currentPassword, newPassword string) error {
	if err := s.hasher.VerifyPassword(currentPassword, auth.User.PasswordHash); err != nil {
		return ErrInvalidCredentials
	}
	passwordHash, err := s.hasher.HashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.store.ChangeUserPassword(ctx, auth.User.ID, passwordHash, s.now())
}

func (s *AppAuthService) issueSession(ctx context.Context, user User) (IssuedAppSession, error) {
	now := s.now()
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

	return IssuedAppSession{
		User:         user,
		Session:      session,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}
