package auth

import (
	"context"
	"time"
)

type Repository interface {
	RegisterUser(ctx context.Context, params RegisterUserParams) (User, error)
	FindUserByUsername(ctx context.Context, usernameNorm string) (User, error)
	FindUserByID(ctx context.Context, userID int64) (User, error)
	// ChangeUserPassword atomically updates the password hash and revokes active app sessions.
	ChangeUserPassword(ctx context.Context, userID int64, passwordHash string, now time.Time) error

	CreateAppSession(ctx context.Context, params CreateAppSessionParams) (AppSession, error)
	FindAppSessionByAccessToken(ctx context.Context, accessTokenDigest string, now time.Time) (AppSession, error)
	RotateAppSessionByRefreshToken(ctx context.Context, params RotateAppSessionParams) (AppSession, error)
	RevokeAppSession(ctx context.Context, sessionID string, now time.Time, reason string) error

	CreateAgentToken(ctx context.Context, params CreateAgentTokenParams) (AgentTokenRecord, error)
	ListAgentTokens(ctx context.Context, userID int64) ([]AgentTokenRecord, error)
	AuthenticateAgentToken(ctx context.Context, tokenDigest string, now time.Time) (AgentTokenRecord, error)
	RevokeAgentToken(ctx context.Context, userID int64, tokenID string, actor string, now time.Time) error
}
