package operator

import (
	"context"
	"errors"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

const OperatorActor = "operator"

var ErrInvalidOperatorRequest = errors.New("invalid operator request")

type OperatorService struct {
	store    Repository
	digester *auth.SecretDigester
	now      func() time.Time
}

func NewOperatorService(store Repository, digester *auth.SecretDigester) *OperatorService {
	return &OperatorService{
		store:    store,
		digester: digester,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *OperatorService) CreateInviteCodes(ctx context.Context, count int, expiresInDays int) ([]string, error) {
	if count <= 0 || expiresInDays <= 0 {
		return nil, ErrInvalidOperatorRequest
	}

	now := s.now()
	expiresAt := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code, err := auth.GenerateInviteCode()
		if err != nil {
			return nil, err
		}
		if _, err := s.store.CreateInviteCode(ctx, auth.CreateInviteCodeParams{
			CodeDigest: s.digester.Digest(code),
			CodeHint:   code[len(code)-2:],
			CreatedBy:  OperatorActor,
			ExpiresAt:  expiresAt,
			Now:        now,
		}); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func (s *OperatorService) DisableInviteCode(ctx context.Context, code string) error {
	normalized, err := auth.NormalizeInviteCode(code)
	if err != nil {
		return err
	}
	return s.store.DisableInviteCode(ctx, s.digester.Digest(normalized), OperatorActor, s.now())
}

func (s *OperatorService) DeleteUser(ctx context.Context, username string) (auth.DeleteUserResult, error) {
	normalized, err := auth.NormalizeUsername(username)
	if err != nil {
		return auth.DeleteUserResult{}, err
	}
	return s.store.DeleteUser(ctx, normalized, OperatorActor, s.now())
}
