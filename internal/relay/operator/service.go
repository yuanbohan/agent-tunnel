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
	store Repository
	now   func() time.Time
}

func NewOperatorService(store Repository) *OperatorService {
	return &OperatorService{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *OperatorService) CreateInviteCodes(ctx context.Context, count int, expiresInDays int) ([]string, error) {
	if count <= 0 || expiresInDays <= 0 {
		return nil, ErrInvalidOperatorRequest
	}

	now := s.now()
	expiresAt := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
	params := make([]auth.CreateInviteCodeParams, 0, count)
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code, err := auth.GenerateInviteCode()
		if err != nil {
			return nil, err
		}
		params = append(params, auth.CreateInviteCodeParams{
			Code:      code,
			CreatedBy: OperatorActor,
			ExpiresAt: expiresAt,
			Now:       now,
		})
		codes = append(codes, code)
	}
	if err := s.store.CreateInviteCodes(ctx, params); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *OperatorService) DisableInviteCode(ctx context.Context, code string) error {
	normalized, err := auth.NormalizeInviteCode(code)
	if err != nil {
		return err
	}
	return s.store.DisableInviteCode(ctx, normalized, OperatorActor, s.now())
}

func (s *OperatorService) ListInviteCodes(ctx context.Context) ([]auth.InviteCodeRecord, error) {
	return s.store.ListInviteCodes(ctx)
}

func (s *OperatorService) DeleteUser(ctx context.Context, username string) (auth.DeleteUserResult, error) {
	normalized, err := auth.NormalizeUsername(username)
	if err != nil {
		return auth.DeleteUserResult{}, err
	}
	return s.store.DeleteUser(ctx, normalized, OperatorActor, s.now())
}

func (s *OperatorService) SetUserSubscriptionTier(ctx context.Context, username, tier string) (auth.User, string, error) {
	normalized, err := auth.NormalizeUsername(username)
	if err != nil {
		return auth.User{}, "", err
	}
	tierNorm, err := auth.NormalizeSubscriptionTier(tier)
	if err != nil {
		return auth.User{}, "", err
	}
	return s.store.SetUserSubscriptionTier(ctx, normalized, tierNorm, OperatorActor, s.now())
}
