package auth

import (
	"errors"
	"time"
)

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrInviteCodeNotFound = errors.New("invite code not found")
	ErrInviteCodeExpired  = errors.New("invite code expired")
	ErrInviteCodeDisabled = errors.New("invite code disabled")
	ErrInviteCodeConsumed = errors.New("invite code consumed")
	ErrAppSessionNotFound = errors.New("app session not found")
	ErrAppSessionExpired  = errors.New("app session expired")
	ErrAppSessionRevoked  = errors.New("app session revoked")
	ErrAgentTokenNotFound = errors.New("agent token not found")
	ErrAgentTokenRevoked  = errors.New("agent token revoked")
)

type User struct {
	ID           int64
	Username     string
	UsernameNorm string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AppSession struct {
	ID                 string
	UserID             int64
	AccessTokenDigest  string
	AccessExpiresAt    time.Time
	RefreshTokenDigest string
	RefreshExpiresAt   time.Time
	RevokedAt          *time.Time
	RevokeReason       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AgentTokenRecord struct {
	ID           string
	UserID       int64
	Name         string
	TokenDigest  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
	RevokeReason string
}

type InviteCodeRecord struct {
	ID               int64
	CodeDigest       string
	CodeHint         string
	CreatedBy        string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	DisabledAt       *time.Time
	DisabledBy       string
	ConsumedAt       *time.Time
	ConsumedByUserID *int64
}

type AuditEvent struct {
	ID                 int64
	EventType          string
	Actor              string
	TargetUserID       *int64
	TargetUsername     string
	TargetAgentTokenID string
	MetadataJSON       string
	CreatedAt          time.Time
}

type RegisterUserParams struct {
	UsernameNorm     string
	InviteCodeDigest string
	PasswordHash     string
	Now              time.Time
}

type CreateAppSessionParams struct {
	ID                 string
	UserID             int64
	AccessTokenDigest  string
	AccessExpiresAt    time.Time
	RefreshTokenDigest string
	RefreshExpiresAt   time.Time
	Now                time.Time
}

type RotateAppSessionParams struct {
	RefreshTokenDigest    string
	NewAccessTokenDigest  string
	NewAccessExpiresAt    time.Time
	NewRefreshTokenDigest string
	NewRefreshExpiresAt   time.Time
	Now                   time.Time
}

type CreateInviteCodeParams struct {
	CodeDigest string
	CodeHint   string
	CreatedBy  string
	ExpiresAt  time.Time
	Now        time.Time
}

type CreateAgentTokenParams struct {
	ID          string
	UserID      int64
	Name        string
	TokenDigest string
	Now         time.Time
}

type DeleteUserResult struct {
	UserID       int64
	UsernameNorm string
}
