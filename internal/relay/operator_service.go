package relay

import (
	"context"
	"errors"
	"time"
)

const OperatorActor = "operator"

var ErrInvalidOperatorRequest = errors.New("invalid operator request")

type OperatorService struct {
	store    Store
	digester *SecretDigester
	now      func() time.Time
}

func NewOperatorService(store Store, digester *SecretDigester) *OperatorService {
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
		code, err := GenerateInviteCode()
		if err != nil {
			return nil, err
		}
		if _, err := s.store.CreateInviteCode(ctx, CreateInviteCodeParams{
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
	normalized, err := NormalizeInviteCode(code)
	if err != nil {
		return err
	}
	return s.store.DisableInviteCode(ctx, s.digester.Digest(normalized), OperatorActor, s.now())
}

func (s *OperatorService) DeleteUser(ctx context.Context, username string) (DeleteUserResult, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return DeleteUserResult{}, err
	}
	return s.store.DeleteUser(ctx, normalized, OperatorActor, s.now())
}
