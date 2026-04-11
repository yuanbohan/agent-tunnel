package relay

import (
	"context"
	"strings"
	"time"
)

var ErrInvalidAgentTokenName = ErrInvalidUsername

type CreatedAgentToken struct {
	Record    AgentTokenRecord
	Plaintext string
}

type AuthenticatedAgentToken struct {
	User  User
	Token AgentTokenRecord
}

type AgentTokenService struct {
	store    Store
	digester *SecretDigester
	now      func() time.Time
}

func NewAgentTokenService(store Store, digester *SecretDigester) *AgentTokenService {
	return &AgentTokenService{
		store:    store,
		digester: digester,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *AgentTokenService) Create(ctx context.Context, userID int64, name string) (CreatedAgentToken, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return CreatedAgentToken{}, ErrInvalidAgentTokenName
	}
	now := s.now()
	tokenID, err := GenerateOpaqueID("agt", 12)
	if err != nil {
		return CreatedAgentToken{}, err
	}
	plaintext, err := GenerateOpaqueToken(24)
	if err != nil {
		return CreatedAgentToken{}, err
	}
	record, err := s.store.CreateAgentToken(ctx, CreateAgentTokenParams{
		ID:          tokenID,
		UserID:      userID,
		Name:        trimmed,
		TokenDigest: s.digester.Digest(plaintext),
		Now:         now,
	})
	if err != nil {
		return CreatedAgentToken{}, err
	}
	return CreatedAgentToken{Record: record, Plaintext: plaintext}, nil
}

func (s *AgentTokenService) List(ctx context.Context, userID int64) ([]AgentTokenRecord, error) {
	return s.store.ListAgentTokens(ctx, userID)
}

func (s *AgentTokenService) Revoke(ctx context.Context, userID int64, tokenID string, actor string) error {
	return s.store.RevokeAgentToken(ctx, userID, tokenID, actor, s.now())
}

func (s *AgentTokenService) Authenticate(ctx context.Context, plaintext string) (AuthenticatedAgentToken, error) {
	record, err := s.store.AuthenticateAgentToken(ctx, s.digester.Digest(plaintext), s.now())
	if err != nil {
		return AuthenticatedAgentToken{}, err
	}
	user, err := s.store.FindUserByID(ctx, record.UserID)
	if err != nil {
		return AuthenticatedAgentToken{}, err
	}
	return AuthenticatedAgentToken{User: user, Token: record}, nil
}
